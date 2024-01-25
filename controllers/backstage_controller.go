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

	"k8s.io/apimachinery/pkg/api/meta"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"janus-idp.io/backstage-operator/pkg/utils"
	corev1 "k8s.io/api/core/v1"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"janus-idp.io/backstage-operator/pkg/model"

	"k8s.io/apimachinery/pkg/types"

	bs "janus-idp.io/backstage-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	BackstageAppLabel = "janus-idp.io/app"
)

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
//+kubebuilder:rbac:groups="",resources=configmaps;secrets;persistentvolumes;persistentvolumeclaims;services,verbs=get;watch;create;update;list;delete
//+kubebuilder:rbac:groups="apps",resources=deployments,verbs=get;watch;create;update;list;delete
//+kubebuilder:rbac:groups="apps",resources=statefulsets,verbs=get;watch;create;update;list;delete
//+kubebuilder:rbac:groups="route.openshift.io",resources=routes;routes/custom-host,verbs=get;watch;create;update;list;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Backstage object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.4/pkg/reconcile
func (r *BackstageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	lg := log.FromContext(ctx)

	lg.V(1).Info(fmt.Sprintf("starting reconciliation (namespace: %q)", req.NamespacedName))

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
	spec, err := r.preprocessSpec(ctx, backstage)
	if err != nil {
		setStatusCondition(&backstage, bs.BackstageConditionTypeDeployed, metav1.ConditionFalse, bs.BackstageConditionReasonFailed, fmt.Sprintf("failed to preprocess backstage spec %s", err))
		return ctrl.Result{}, fmt.Errorf("failed to preprocess backstage spec %w", err)
	}

	// This creates array of model objects to be reconsiled
	bsModel, err := model.InitObjects(ctx, backstage, spec, r.OwnsRuntime, r.IsOpenShift)
	if err != nil {
		setStatusCondition(&backstage, bs.BackstageConditionTypeDeployed, metav1.ConditionFalse, bs.BackstageConditionReasonFailed, fmt.Sprintf("failed to initialize backstage model %s", err))
		return ctrl.Result{}, fmt.Errorf("failed to initialize backstage model %w", err)
	}

	//TODO, do it on model? (need to send Scheme to InitObjects just for this)
	if r.OwnsRuntime {
		for _, obj := range bsModel.Objects {
			if err = controllerutil.SetControllerReference(&backstage, obj.Object(), r.Scheme); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set owner reference: %s", err)
			}
		}
	}

	err = r.applyObjects(ctx, bsModel.Objects)
	if err != nil {
		setStatusCondition(&backstage, bs.BackstageConditionTypeDeployed, metav1.ConditionFalse, bs.BackstageConditionReasonFailed, fmt.Sprintf("failed to apply backstage objects %s", err))
		return ctrl.Result{}, fmt.Errorf("failed to apply backstage objects %w", err)
	}

	if err := r.cleanObjects(ctx, backstage); err != nil {
		setStatusCondition(&backstage, bs.BackstageConditionTypeDeployed, metav1.ConditionFalse, bs.BackstageConditionReasonFailed, fmt.Sprintf("failed to clean backstage objects %s", err))
		return ctrl.Result{}, fmt.Errorf("failed to clean backstage objects %w", err)
	}

	setStatusCondition(&backstage, bs.BackstageConditionTypeDeployed, metav1.ConditionTrue, bs.BackstageConditionReasonDeployed, "")

	return ctrl.Result{}, nil
}

func (r *BackstageReconciler) applyObjects(ctx context.Context, objects []model.BackstageObject) error {

	lg := log.FromContext(ctx)

	for _, obj := range objects {

		if err := r.Get(ctx, types.NamespacedName{Name: obj.Object().GetName(), Namespace: obj.Object().GetNamespace()}, obj.EmptyObject()); err != nil {
			if !errors.IsNotFound(err) {
				return fmt.Errorf("failed to get object: %w", err)
			}

			if err := r.Create(ctx, obj.Object()); err != nil {
				return fmt.Errorf("failed to create object %w", err)
			}

			lg.V(1).Info("Create object ", "obj", obj.Object().GetName())
			continue
		}

		if err := r.Update(ctx, obj.Object()); err != nil {
			return fmt.Errorf("failed to update object %s: %w", obj.Object().GetName(), err)
		}

		// [GA] do not remove it
		//if obj, ok := obj.(*model.BackstageDeployment); ok {
		//	depl := obj.Object().(*appsv1.Deployment)
		//	str := fmt.Sprintf("%v", depl.Spec)
		//	lg.V(1).Info("Update object ", "obj", str)
		//	//obj.Object().GetName(), "resourceVersion", obj.Object().GetResourceVersion(), "generation", obj.Object().GetGeneration())
		//
		//}

	}
	return nil
}

func (r *BackstageReconciler) cleanObjects(ctx context.Context, backstage bs.Backstage) error {
	// check if local database disabled, respective objects have to deleted/unowned
	if !backstage.Spec.IsLocalDbEnabled() {
		ss := &appsv1.StatefulSet{}
		if err := r.Get(ctx, types.NamespacedName{Name: utils.GenerateRuntimeObjectName(backstage.Name, "db-statefulset"), Namespace: backstage.Namespace}, ss); err == nil {
			if err := r.Delete(ctx, ss); err != nil {
				return fmt.Errorf("failed to delete %s: %w", ss.Name, err)
			}
		}
		dbService := &corev1.Service{}
		if err := r.Get(ctx, types.NamespacedName{Name: utils.GenerateRuntimeObjectName(backstage.Name, "db-service"), Namespace: backstage.Namespace}, dbService); err == nil {
			if err := r.Delete(ctx, dbService); err != nil {
				return fmt.Errorf("failed to delete %s: %w", dbService.Name, err)
			}
		}
		dbSecret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: utils.GenerateRuntimeObjectName(backstage.Name, "db-secret"), Namespace: backstage.Namespace}, dbSecret); err == nil {
			if err := r.Delete(ctx, dbSecret); err != nil {
				return fmt.Errorf("failed to delete %s: %w", dbSecret.Name, err)
			}
		}
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

// SetupWithManager sets up the controller with the Manager.
func (r *BackstageReconciler) SetupWithManager(mgr ctrl.Manager, log logr.Logger) error {

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
