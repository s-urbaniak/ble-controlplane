// SPDX-License-Identifier: Apache-2.0

// Package storage evaluates Kubernetes self-subject access reviews in memory.
package storage

import (
	"context"
	"errors"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1validation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
)

type REST struct {
	authorizer authorizer.UnconditionalAuthorizer
}

var (
	_ rest.Creater              = (*REST)(nil)
	_ rest.Scoper               = (*REST)(nil)
	_ rest.SingularNameProvider = (*REST)(nil)
)

func NewREST(auth authorizer.UnconditionalAuthorizer) *REST { return &REST{authorizer: auth} }

func (*REST) New() runtime.Object     { return &authorizationv1.SelfSubjectAccessReview{} }
func (*REST) NamespaceScoped() bool   { return false }
func (*REST) GetSingularName() string { return "selfsubjectaccessreview" }
func (*REST) Destroy()                {}

func (r *REST) Create(ctx context.Context, obj runtime.Object, validate rest.ValidateObjectFunc, _ *metav1.CreateOptions) (runtime.Object, error) {
	review, ok := obj.(*authorizationv1.SelfSubjectAccessReview)
	if !ok {
		return nil, apierrors.NewBadRequest("expected a SelfSubjectAccessReview")
	}
	if errs := validateReview(review); len(errs) != 0 {
		return nil, apierrors.NewInvalid(authorizationv1.SchemeGroupVersion.WithKind("SelfSubjectAccessReview").GroupKind(), "", errs)
	}
	identity, ok := request.UserFrom(ctx)
	if !ok || identity == nil {
		return nil, apierrors.NewBadRequest("no user present on request")
	}
	if validate != nil {
		if err := validate(ctx, review.DeepCopy()); err != nil {
			return nil, err
		}
	}

	attrs := authorizer.AttributesRecord{User: identity}
	if resource := review.Spec.ResourceAttributes; resource != nil {
		attrs.ResourceRequest = true
		attrs.Verb = resource.Verb
		attrs.Namespace = resource.Namespace
		attrs.APIGroup = resource.Group
		attrs.APIVersion = resource.Version
		if attrs.APIVersion == "" {
			attrs.APIVersion = "*"
		}
		attrs.Resource = resource.Resource
		attrs.Subresource = resource.Subresource
		attrs.Name = resource.Name
		attrs.FieldSelectorRequirements, attrs.FieldSelectorParsingErr = fieldRequirements(resource.FieldSelector)
		attrs.LabelSelectorRequirements, attrs.LabelSelectorParsingErr = labelRequirements(resource.LabelSelector)
	} else {
		attrs.Verb = review.Spec.NonResourceAttributes.Verb
		attrs.Path = review.Spec.NonResourceAttributes.Path
	}

	decision, reason, err := r.authorizer.Authorize(ctx, attrs)
	var evaluationErrors []string
	if err != nil {
		evaluationErrors = append(evaluationErrors, err.Error())
	}
	if attrs.FieldSelectorParsingErr != nil {
		evaluationErrors = append(evaluationErrors, selectorEvaluationError("fieldSelector", len(attrs.FieldSelectorRequirements)))
	}
	if attrs.LabelSelectorParsingErr != nil {
		evaluationErrors = append(evaluationErrors, selectorEvaluationError("labelSelector", len(attrs.LabelSelectorRequirements)))
	}
	review.Status = authorizationv1.SubjectAccessReviewStatus{
		Allowed:         decision == authorizer.DecisionAllow,
		Denied:          decision == authorizer.DecisionDeny,
		Reason:          reason,
		EvaluationError: strings.Join(evaluationErrors, "; "),
	}
	return review, nil
}

func validateReview(review *authorizationv1.SelfSubjectAccessReview) field.ErrorList {
	var errs field.ErrorList
	metadata := review.ObjectMeta
	metadata.ManagedFields = nil
	if !equality.Semantic.DeepEqual(metadata, metav1.ObjectMeta{}) {
		errs = append(errs, field.Invalid(field.NewPath("metadata"), metadata, "must be empty"))
	}
	resource := review.Spec.ResourceAttributes
	if (resource == nil) == (review.Spec.NonResourceAttributes == nil) {
		errs = append(errs, field.Invalid(field.NewPath("spec"), nil, "exactly one of resourceAttributes and nonResourceAttributes must be specified"))
	}
	if resource == nil {
		return errs
	}
	path := field.NewPath("spec", "resourceAttributes")
	if selector := resource.FieldSelector; selector != nil {
		selectorPath := path.Child("fieldSelector")
		errs = append(errs, validateSelectorEncoding(selector.RawSelector, len(selector.Requirements), selectorPath)...)
		opts := metav1validation.FieldSelectorValidationOptions{AllowUnknownOperatorInRequirement: true}
		for i, requirement := range selector.Requirements {
			errs = append(errs, metav1validation.ValidateFieldSelectorRequirement(requirement, opts, selectorPath.Child("requirements").Index(i))...)
		}
	}
	if selector := resource.LabelSelector; selector != nil {
		selectorPath := path.Child("labelSelector")
		errs = append(errs, validateSelectorEncoding(selector.RawSelector, len(selector.Requirements), selectorPath)...)
		opts := metav1validation.LabelSelectorValidationOptions{AllowUnknownOperatorInRequirement: true}
		for i, requirement := range selector.Requirements {
			errs = append(errs, metav1validation.ValidateLabelSelectorRequirement(requirement, opts, selectorPath.Child("requirements").Index(i))...)
		}
	}
	return errs
}

func validateSelectorEncoding(raw string, count int, path *field.Path) field.ErrorList {
	if raw != "" && count != 0 {
		return field.ErrorList{field.Invalid(path.Child("rawSelector"), raw, "may not be specified with requirements")}
	}
	if raw == "" && count == 0 {
		return field.ErrorList{field.Required(path.Child("requirements"), "specify requirements or rawSelector")}
	}
	return nil
}

func fieldRequirements(selector *authorizationv1.FieldSelectorAttributes) (fields.Requirements, error) {
	if selector == nil {
		return nil, nil
	}
	if selector.RawSelector != "" {
		parsed, err := fields.ParseSelector(selector.RawSelector)
		if err != nil {
			return nil, err
		}
		return parsed.Requirements(), nil
	}
	var requirements fields.Requirements
	var parseError error
	for _, expr := range selector.Requirements {
		var operator selection.Operator
		switch expr.Operator {
		case metav1.FieldSelectorOpIn:
			operator = selection.Equals
		case metav1.FieldSelectorOpNotIn:
			operator = selection.NotEquals
		}
		if operator == "" || len(expr.Values) != 1 {
			// Dropping an unsupported requirement broadens the check because the requirements are ANDed.
			parseError = errors.New("unsupported field selector requirement")
			continue
		}
		requirements = append(requirements, fields.Requirement{Field: expr.Key, Operator: operator, Value: expr.Values[0]})
	}
	return requirements, parseError
}

func labelRequirements(selector *authorizationv1.LabelSelectorAttributes) (labels.Requirements, error) {
	if selector == nil {
		return nil, nil
	}
	if selector.RawSelector != "" {
		parsed, err := labels.Parse(selector.RawSelector)
		if err != nil {
			return nil, err
		}
		requirements, _ := parsed.Requirements()
		return requirements, nil
	}
	var requirements labels.Requirements
	var parseError error
	for _, expr := range selector.Requirements {
		parsed, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{expr}})
		if err != nil {
			parseError = err
			continue
		}
		expressionRequirements, _ := parsed.Requirements()
		requirements = append(requirements, expressionRequirements...)
	}
	return requirements, parseError
}

func selectorEvaluationError(selector string, requirementCount int) string {
	path := "spec.resourceAttributes." + selector
	if requirementCount != 0 {
		return path + " partially ignored due to parse error"
	}
	return path + " ignored due to parse error"
}
