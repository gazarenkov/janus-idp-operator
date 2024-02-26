//
// Copyright (c) 2023 Red Hat, Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"

	openshift "github.com/openshift/api/route/v1"

	"k8s.io/apimachinery/pkg/api/meta"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1 "k8s.io/api/core/v1"

	appsv1 "k8s.io/api/apps/v1"

	"janus-idp.io/backstage-operator/pkg/model"

	bs "janus-idp.io/backstage-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var recNumber = 0

// BackstageReconciler reconciles a Backstage object
type BackstageReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// If true, Backstage Controller always sync the state of runtime objects created
	// otherwise, runtime objects can be re-configured independently
	OwnsRuntime bool

	// Namespace allows to restrict the reconciliation to this particular namespace,
	// and ignore requests from other namespaces.
	// This is mostly useful for our tests, to overcome a limitation of EnvTest about namespace deletion.
	Namespace string

	IsOpenShift bool
}

//+kubebuilder:rbac:groups=janus-idp.io,resources=backstages,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=janus-idp.io,resources=backstages/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=janus-idp.io,resources=backstages/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=configmaps;secrets;persistentvolumes;persistentvolumeclaims;services,verbs=get;watch;create;update;list;delete;patch
//+kubebuilder:rbac:groups="apps",resources=deployments,verbs=get;watch;create;update;list;delete;patch
//+kubebuilder:rbac:groups="apps",resources=statefulsets,verbs=get;watch;create;update;list;delete;patch
//+kubebuilder:rbac:groups="route.openshift.io",resources=routes;routes/custom-host,verbs=get;watch;create;update;list;delete;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.4/pkg/reconcile
func (r *BackstageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	lg := log.FromContext(ctx)

	recNumber = recNumber + 1
	lg.V(1).Info(fmt.Sprintf("starting reconciliation (namespace: %q), number %d", req.NamespacedName, recNumber))

	// Ignore requests for other namespaces, if specified.
	// This is mostly useful for our tests, to overcome a limitation of EnvTest about namespace deletion.
	// More details on https://book.kubebuilder.io/reference/envtest.html#namespace-usage-limitation
	if r.Namespace != "" && req.Namespace != r.Namespace {
		return ctrl.Result{}, nil
	}

	backstage := bs.Backstage{}
	if err := r.Get(ctx, req.NamespacedName, &backstage); err != nil {
		if errors.IsNotFound(err) {
			lg.Info("backstage gone from the namespace")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to load backstage deployment from the cluster: %w", err)
	}

	// This update will make sure the status is always updated in case of any errors or successful result
	defer func(bs *bs.Backstage) {
		if err := r.Client.Status().Update(ctx, bs); err != nil {
			if errors.IsConflict(err) {
				lg.V(1).Info("Backstage object modified, retry syncing status", "Backstage Object", bs)
				return
			}
			lg.Error(err, "Error updating the Backstage resource status", "Backstage Object", bs)
		}
	}(&backstage)

	if len(backstage.Status.Conditions) == 0 {
		setStatusCondition(&backstage, bs.BackstageConditionTypeDeployed, metav1.ConditionFalse, bs.BackstageConditionReasonInProgress, "Deployment process started")
	}

	// 1. Preliminary read and prepare external config objects from the specs (configMaps, Secrets)
	// 2. Make some validation to fail fast
	rawConfig, err := r.rawConfigMap(ctx, backstage)
	if err != nil {
		return ctrl.Result{}, errorAndStatus(&backstage, "failed to preprocess backstage raw spec", err)
	}

	appConfigs, err := r.appConfigMaps(ctx, backstage)
	if err != nil {
		return ctrl.Result{}, errorAndStatus(&backstage, "failed to preprocess backstage spec app-configs", err)
	}

	// This creates array of model objects to be reconsiled
	bsModel, err := model.InitObjects(ctx, backstage, rawConfig, appConfigs, r.OwnsRuntime, r.IsOpenShift, r.Scheme)
	if err != nil {
		return ctrl.Result{}, errorAndStatus(&backstage, "failed to initialize backstage model", err)
	}

	//if backstage.Spec.IsLocalDbEnabled() && !backstage.Spec.IsAuthSecretSpecified() {
	//	if err := dbsecret.Generate(ctx, r.Client, backstage, bsModel.LocalDbService, r.Scheme); err != nil {
	//		return ctrl.Result{}, errorAndStatus(&backstage, "failed to generate db-secret", err)
	//	}
	//}

	err = r.applyObjects(ctx, bsModel.RuntimeObjects)
	if err != nil {
		return ctrl.Result{}, errorAndStatus(&backstage, "failed to apply backstage objects", err)
	}

	if err := r.cleanObjects(ctx, backstage); err != nil {
		return ctrl.Result{}, errorAndStatus(&backstage, "failed to clean backstage objects ", err)
	}

	setStatusCondition(&backstage, bs.BackstageConditionTypeDeployed, metav1.ConditionTrue, bs.BackstageConditionReasonDeployed, "")

	return ctrl.Result{}, nil
}

func errorAndStatus(backstage *bs.Backstage, msg string, err error) error {
	setStatusCondition(backstage, bs.BackstageConditionTypeDeployed, metav1.ConditionFalse, bs.BackstageConditionReasonFailed, fmt.Sprintf("%s %s", msg, err))
	return fmt.Errorf("%s %w", msg, err)
}

func (r *BackstageReconciler) applyObjects(ctx context.Context, objects []model.RuntimeObject) error {

	lg := log.FromContext(ctx)

	for _, obj := range objects {

		baseObject := obj.EmptyObject()
		baseObject.SetName(obj.Object().GetName())
		baseObject.SetNamespace(obj.Object().GetNamespace())

		// do not read Secrets
		if _, ok := obj.Object().(*corev1.Secret); ok {
			// try to create
			if err := r.Create(ctx, obj.Object()); err != nil {
				if !errors.IsAlreadyExists(err) {
					return fmt.Errorf("failed to create secret: %w", err)
				}
				//if DBSecret - nothing to do, it is not for update
				if _, ok := obj.(*model.DbSecret); ok {
					continue
				}
			} else {
				lg.V(1).Info("Create secret ", "obj", obj.Object().GetName())
				continue
			}

		} else {
			if err := r.Get(ctx, types.NamespacedName{Name: obj.Object().GetName(), Namespace: obj.Object().GetNamespace()}, baseObject); err != nil {
				if !errors.IsNotFound(err) {
					return fmt.Errorf("failed to get object: %w", err)
				}

				if err := r.Create(ctx, obj.Object()); err != nil {
					return fmt.Errorf("failed to create object %w", err)
				}

				lg.V(1).Info("Create object ", "obj", obj.Object().GetName())
				continue
			}
		}

		//baseObject := obj.EmptyObject()
		//if err := r.Get(ctx, types.NamespacedName{Name: obj.Object().GetName(), Namespace: obj.Object().GetNamespace()}, baseObject); err != nil {
		//	if !errors.IsNotFound(err) {
		//		return fmt.Errorf("failed to get object: %w", err)
		//	}
		//
		//	if err := r.Create(ctx, obj.Object()); err != nil {
		//		return fmt.Errorf("failed to create object %w", err)
		//	}
		//
		//	lg.V(1).Info("Create object ", "obj", obj.Object().GetName())
		//	continue
		//}

		// FIXME
		if _, ok := obj.Object().(*appsv1.Deployment); ok {
			obj.Object().SetAnnotations(baseObject.GetAnnotations())
		}

		// needed for openshift.Route only, it yells otherwise
		obj.Object().SetResourceVersion(baseObject.GetResourceVersion())

		if err := r.Patch(ctx, obj.Object(), client.MergeFrom(baseObject)); err != nil {
			return fmt.Errorf("failed to patch object %s: %w", obj.Object().GetResourceVersion(), err)
		}

		lg.V(1).Info("Patch object ", "", obj.Object().GetName())

	}
	return nil
}

func (r *BackstageReconciler) cleanObjects(ctx context.Context, backstage bs.Backstage) error {

	const failedToCleanup = "failed to cleanup runtime"
	// check if local database disabled, respective objects have to deleted/unowned
	if !backstage.Spec.IsLocalDbEnabled() {
		if err := r.tryToDelete(ctx, &appsv1.StatefulSet{}, model.DbStatefulSetName(backstage.Name), backstage.Namespace); err != nil {
			return fmt.Errorf("%s %w", failedToCleanup, err)
		}
		if err := r.tryToDelete(ctx, &corev1.Service{}, model.DbServiceName(backstage.Name), backstage.Namespace); err != nil {
			return fmt.Errorf("%s %w", failedToCleanup, err)
		}
		if err := r.tryToDelete(ctx, &corev1.Secret{}, model.DbSecretDefaultName(backstage.Name), backstage.Namespace); err != nil {
			return fmt.Errorf("%s %w", failedToCleanup, err)
		}
	}

	//// check if route disabled, respective objects have to deleted/unowned
	if r.IsOpenShift && !backstage.Spec.IsRouteEnabled() {
		if err := r.tryToDelete(ctx, &openshift.Route{}, model.RouteName(backstage.Name), backstage.Namespace); err != nil {
			return fmt.Errorf("%s %w", failedToCleanup, err)
		}
	}

	return nil
}

// tryToDelete tries to delete the object by name and namespace, does not throw error if object not found
func (r *BackstageReconciler) tryToDelete(ctx context.Context, obj client.Object, name string, ns string) error {
	obj.SetName(name)
	obj.SetNamespace(ns)
	if err := r.Delete(ctx, obj); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete %s: %w", name, err)
	}
	return nil
}

func setStatusCondition(backstage *bs.Backstage, condType bs.BackstageConditionType, status metav1.ConditionStatus, reason bs.BackstageConditionReason, msg string) {
	meta.SetStatusCondition(&backstage.Status.Conditions, metav1.Condition{
		Type:               string(condType),
		Status:             status,
		LastTransitionTime: metav1.Time{},
		Reason:             string(reason),
		Message:            msg,
	})
}

// need pre-read raw config (if any) for runtime objects
func (r *BackstageReconciler) rawConfigMap(ctx context.Context, backstage bs.Backstage) (map[string]string, error) {
	//lg := log.FromContext(ctx)

	bsSpec := backstage.Spec
	ns := backstage.Namespace
	result := map[string]string{}

	// Process RawRuntimeConfig
	if backstage.Spec.RawRuntimeConfig != "" {
		cm := corev1.ConfigMap{}
		if err := r.Get(ctx, types.NamespacedName{Name: bsSpec.RawRuntimeConfig, Namespace: ns}, &cm); err != nil {
			return nil, fmt.Errorf("failed to load rawConfig %s: %w", bsSpec.RawRuntimeConfig, err)
		}
		for key, value := range cm.Data {
			result[key] = value
		}
	}

	return result, nil
}

// need pre-read app-configs to be able to put --config argument
func (r *BackstageReconciler) appConfigMaps(ctx context.Context, backstage bs.Backstage) ([]model.SpecifiedConfigMap, error) {
	// Process AppConfigs
	result := []model.SpecifiedConfigMap{}
	if backstage.Spec.Application != nil && backstage.Spec.Application.AppConfig != nil {
		for _, ac := range backstage.Spec.Application.AppConfig.ConfigMaps {
			cm := corev1.ConfigMap{}
			if err := r.Get(ctx, types.NamespacedName{Name: ac.Name, Namespace: backstage.Namespace}, &cm); err != nil {
				return nil, fmt.Errorf("failed to get configMap %s: %w", ac.Name, err)
			}
			result = append(result, model.SpecifiedConfigMap{
				ConfigMap: cm,
				Key:       ac.Key,
			})
		}
	}
	return result, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BackstageReconciler) SetupWithManager(mgr ctrl.Manager) error {

	builder := ctrl.NewControllerManagedBy(mgr).
		For(&bs.Backstage{})

	// [GA] do not remove it
	//if r.OwnsRuntime {
	//	builder.Owns(&appsv1.Deployment{}).
	//		Owns(&corev1.Service{}).
	//		Owns(&appsv1.StatefulSet{})
	//}

	return builder.Complete(r)
}
