/*
Copyright 2026 The pizzaplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package credentials manages persistent local credentials for the control plane.
package credentials

import (
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	certutil "k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/keyutil"
)

const ClientUser = "ble-controlplane-client"

type Files struct {
	CA, CAKey, ServerCert, ServerKey string
	clientCert, clientKey            string
}

// Ensure preserves the CA on restart and renews API certificates for the host.
// The caller must hold the data-directory lock.
func Ensure(dir string, hosts ...string) (Files, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Files{}, err
	}
	f := Files{
		CA: filepath.Join(dir, "ca.crt"), CAKey: filepath.Join(dir, "ca.key"),
		ServerCert: filepath.Join(dir, "server.crt"), ServerKey: filepath.Join(dir, "server.key"),
		clientCert: filepath.Join(dir, "client.crt"), clientKey: filepath.Join(dir, "client.key"),
	}
	if _, err := os.Stat(f.CA); os.IsNotExist(err) {
		// Preserve the partial-CA guard: never silently replace an existing key.
		if _, e := os.Stat(f.CAKey); !os.IsNotExist(e) {
			return f, fmt.Errorf("CA certificate missing but CA key exists in %s", dir)
		}
		key, keyPEM, err := newKey()
		if err != nil {
			return f, err
		}
		ca, err := certutil.NewSelfSignedCACert(certutil.Config{CommonName: "ble-controlplane-ca", NotBefore: time.Now().Add(-time.Hour)}, key)
		if err != nil {
			return f, err
		}
		certPEM, err := certutil.EncodeCertificates(ca)
		if err != nil {
			return f, err
		}
		if err := WritePrivate(f.CAKey, keyPEM); err != nil {
			return f, err
		}
		if err := WritePrivate(f.CA, certPEM); err != nil {
			return f, err
		}
	} else if err != nil {
		return f, err
	}
	certs, err := certutil.CertsFromFile(f.CA)
	if err != nil {
		return f, err
	}
	ca := certs[0]
	privateKey, err := keyutil.PrivateKeyFromFile(f.CAKey)
	if err != nil {
		return f, err
	}
	caKey, ok := privateKey.(crypto.Signer)
	if !ok {
		return f, fmt.Errorf("unsupported CA key type %T", privateKey)
	}
	public, ok := ca.PublicKey.(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !ca.IsCA || !public.Equal(caKey.Public()) {
		return f, fmt.Errorf("CA certificate/key mismatch")
	}
	for _, leaf := range []struct {
		cert, key, user string
		server          bool
	}{
		{f.ServerCert, f.ServerKey, "ble-controlplane", true},
		{f.clientCert, f.clientKey, ClientUser, false},
	} {
		key, keyPEM, err := newKey()
		if err != nil {
			return f, err
		}
		cfg := certutil.Config{CommonName: leaf.user, NotBefore: time.Now().Add(-time.Hour), Usages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		if leaf.server {
			cfg.Usages = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			cfg.AltNames = certutil.AltNames{DNSNames: []string{"localhost"}, IPs: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
			for _, host := range hosts {
				if ip := net.ParseIP(host); ip != nil {
					cfg.AltNames.IPs = append(cfg.AltNames.IPs, ip)
				} else {
					cfg.AltNames.DNSNames = append(cfg.AltNames.DNSNames, host)
				}
			}
		}
		cert, err := newSignedCert(cfg, key, ca, caKey)
		if err != nil {
			return f, err
		}
		certPEM, err := certutil.EncodeCertificates(cert)
		if err != nil {
			return f, err
		}
		if err := WritePrivate(leaf.key, keyPEM); err != nil {
			return f, err
		}
		if err := WritePrivate(leaf.cert, certPEM); err != nil {
			return f, err
		}
	}
	return f, nil
}

func newKey() (crypto.Signer, []byte, error) {
	data, err := keyutil.MakeEllipticPrivateKeyPEM()
	if err != nil {
		return nil, nil, err
	}
	key, err := keyutil.ParsePrivateKeyPEM(data)
	if err != nil {
		return nil, nil, err
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, nil, fmt.Errorf("unsupported private key type %T", key)
	}
	return signer, data, nil
}

// newSignedCert follows kubeadm's NewSignedCert (Apache-2.0). client-go exposes
// CA creation but no existing-CA leaf signer; keep this adapter here so the
// minimal server does not acquire kubeadm or Kubernetes monolith dependencies.
func newSignedCert(cfg certutil.Config, key crypto.Signer, ca *x509.Certificate, caKey crypto.Signer) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1)))
	if err != nil {
		return nil, err
	}
	serial.Add(serial, big.NewInt(1))
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cfg.CommonName, Organization: cfg.Organization},
		NotBefore:    cfg.NotBefore, NotAfter: time.Now().AddDate(1, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: cfg.Usages,
		BasicConstraintsValid: true,
		DNSNames:              cfg.AltNames.DNSNames, IPAddresses: cfg.AltNames.IPs,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, key.Public(), caKey)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

// WritePrivate atomically replaces a private file without a permissive intermediate.
func WritePrivate(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".ble-controlplane-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (f Files) WriteKubeconfig(path, endpoint, namespace string) error {
	ca, err := os.ReadFile(f.CA)
	if err != nil {
		return err
	}
	cert, err := os.ReadFile(f.clientCert)
	if err != nil {
		return err
	}
	key, err := os.ReadFile(f.clientKey)
	if err != nil {
		return err
	}
	c := clientcmdapi.NewConfig()
	c.Clusters["ble-controlplane"] = &clientcmdapi.Cluster{Server: endpoint, CertificateAuthorityData: ca}
	c.AuthInfos[ClientUser] = &clientcmdapi.AuthInfo{ClientCertificateData: cert, ClientKeyData: key}
	c.Contexts["ble-controlplane"] = &clientcmdapi.Context{Cluster: "ble-controlplane", AuthInfo: ClientUser, Namespace: namespace}
	c.CurrentContext = "ble-controlplane"
	data, err := clientcmd.Write(*c)
	if err != nil {
		return err
	}
	return WritePrivate(path, data)
}
