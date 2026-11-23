// Copyright 2026 The pizzaplane Authors. SPDX-License-Identifier: Apache-2.0

import CoreBluetooth
import Darwin
import Foundation

final class Scanner: NSObject, CBCentralManagerDelegate {
    private var central: CBCentralManager!

    func start() {
        central = CBCentralManager(delegate: self, queue: nil)
    }

    func centralManagerDidUpdateState(_ central: CBCentralManager) {
        switch central.state {
        case .poweredOn:
            central.scanForPeripherals(
                withServices: nil,
                options: [CBCentralManagerScanOptionAllowDuplicatesKey: true]
            )
            send(["ready": true])
        case .unknown:
            break
        case .resetting:
            fail("Bluetooth is resetting")
        case .poweredOff:
            fail("Bluetooth is turned off")
        case .unauthorized:
            fail("Bluetooth permission was denied. Allow access in System Settings → Privacy & Security → Bluetooth")
        case .unsupported:
            fail("BLE scanning is unsupported on this Mac")
        @unknown default:
            fail("the Bluetooth adapter reported an unknown state")
        }
    }

    func centralManager(
        _ central: CBCentralManager,
        didDiscover peripheral: CBPeripheral,
        advertisementData: [String: Any],
        rssi RSSI: NSNumber
    ) {
        var observation: [String: Any] = ["key": peripheral.identifier.uuidString]
        if let name = advertisementData[CBAdvertisementDataLocalNameKey] as? String ?? peripheral.name {
            observation["localName"] = name
        }
        if RSSI.intValue != 127 {
            observation["rssi"] = RSSI.intValue
        }
        if let power = advertisementData[CBAdvertisementDataTxPowerLevelKey] as? NSNumber {
            observation["txPower"] = power.intValue
        }
        if let connectable = advertisementData[CBAdvertisementDataIsConnectable] as? NSNumber {
            observation["connectable"] = connectable.boolValue
        }
        if let services = advertisementData[CBAdvertisementDataServiceUUIDsKey] as? [CBUUID] {
            observation["serviceUUIDs"] = services.map(\.uuidString)
        }
        if let data = advertisementData[CBAdvertisementDataManufacturerDataKey] as? Data, data.count >= 2 {
            let company = Int(data[data.startIndex]) | (Int(data[data.startIndex + 1]) << 8)
            observation["companyIDs"] = [company]
        }
        send(observation)
    }

    private func send(_ message: [String: Any]) {
        guard var bytes = try? JSONSerialization.data(withJSONObject: message) else {
            exit(EXIT_FAILURE)
        }
        bytes.append(0x0a)
        do {
            try FileHandle.standardOutput.write(contentsOf: bytes)
        } catch {
            exit(EXIT_FAILURE)
        }
    }

    private func fail(_ reason: String) {
        send(["error": reason])
        exit(EXIT_FAILURE)
    }
}

let scanner = Scanner()
scanner.start()
RunLoop.main.run()
