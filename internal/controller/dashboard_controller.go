/*
Copyright 2025.

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

package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	testgridv1alpha1 "sigs.k8s.io/signalhound/api/v1alpha1"
	"sigs.k8s.io/signalhound/internal/testgrid"
)

// DashboardReconciler reconciles a Dashboard object
type DashboardReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	log    logr.Logger
}

// +kubebuilder:rbac:groups=testgrid.holdmybeer.io,resources=dashboards,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=testgrid.holdmybeer.io,resources=dashboards/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=testgrid.holdmybeer.io,resources=dashboards/finalizers,verbs=update

// Reconcile loops against the dashboard reconciler and set the final object status.
func (r *DashboardReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.log = logf.FromContext(ctx).WithValues("resource", req.NamespacedName)

	var dashboard testgridv1alpha1.Dashboard
	if err := r.Get(ctx, req.NamespacedName, &dashboard); err != nil {
		r.log.Error(err, "unable to fetch dashboard")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	grid := testgrid.NewTestGrid(testgrid.URL)
	dashboardSummaries, err := grid.FetchTabSummary(dashboard.Spec.DashboardTab, testgridv1alpha1.ERROR_STATUSES)
	if err != nil {
		r.log.Error(err, "error fetching summary from endpoint.")
		return ctrl.Result{}, err
	}

	// set the dashboard summary on status if an update happened
	if r.shouldRefresh(dashboard.Status, dashboardSummaries) {
		dashboard.Status.DashboardSummary = dashboardSummaries
		dashboard.Status.LastUpdate = metav1.Now()

		r.log.Info("updating dashboard object status.")
		if err := r.Status().Update(ctx, &dashboard); err != nil {
			r.log.Error(err, "unable to update dashboard status")
			return ctrl.Result{}, err
		}

		// create or update the tab summary board if necessary
		for _, dashSummary := range dashboardSummaries {
			tabName := dashSummary.DashboardTab.TabName
			configMapKey := client.ObjectKey{
				Namespace: req.Namespace,
				Name:      fmt.Sprintf("%s-%s", dashSummary.DashboardName, tabName),
			}

			var tab *testgridv1alpha1.DashboardTab
			if tab, err = grid.FetchTabTests(&dashSummary, dashboard.Spec.MinFlakes, dashboard.Spec.MinFailures); err != nil {
				r.log.Error(err, "error fetching table", "tab", tabName)
				continue
			}

			configMap, err := buildTestConfigMap(configMapKey, tab)
			if err != nil {
				r.log.Error(err, "failed to build ConfigMap", "name", configMapKey.Name)
				continue
			}
			if err = r.createOrUpdateConfigmap(ctx, configMapKey, configMap); err != nil {
				r.log.Error(err, "unable to create update a configmap")
				continue
			}
		}
	}

	return ctrl.Result{}, nil
}

// createOrUpdateConfigmap creates or updates ConfigMaps for each dashboard tab
// containing the test data retrieved from TestGrid.
func (r *DashboardReconciler) createOrUpdateConfigmap(
	ctx context.Context,
	configMapKey client.ObjectKey,
	configMap *v1.ConfigMap,
) (err error) {
	if !r.doesExistsConfigmap(ctx, configMapKey) {
		if err := r.Create(ctx, configMap); err != nil {
			return fmt.Errorf("failed to create ConfigMap %s: %w", configMapKey.Name, err)
		}
		r.log.Info("created ConfigMap", "configmap", configMapKey.Name)

	} else {
		if err = r.Update(ctx, configMap); err != nil {
			return fmt.Errorf("failed to update ConfigMap %s: %w", configMapKey.Name, err)
		}
		r.log.V(1).Info("updated ConfigMap", "configmap", configMapKey.Name)

	}
	return nil
}

func (r *DashboardReconciler) doesExistsConfigmap(ctx context.Context, key client.ObjectKey) bool {
	var cm = &v1.ConfigMap{}
	if err := r.Get(ctx, key, cm); err != nil {
		if apierrors.IsNotFound(err) {
			return true
		}
	}
	return false
}

// shouldRefresh determines if it's time to refresh the dashboard data
func (r *DashboardReconciler) shouldRefresh(dashboardStatus testgridv1alpha1.DashboardStatus, summary []testgridv1alpha1.DashboardSummary) bool {
	if reflect.DeepEqual(dashboardStatus.DashboardSummary, summary) {
		return false
	}
	if dashboardStatus.LastUpdate.IsZero() {
		return true
	}
	refreshInterval := time.Duration(1) * time.Minute // should at least wait for 1 minute
	return time.Since(dashboardStatus.LastUpdate.Time) >= refreshInterval
}

func buildTestConfigMap(key client.ObjectKey, tab *testgridv1alpha1.DashboardTab) (*v1.ConfigMap, error) {
	data, err := json.Marshal(tab)
	if err != nil {
		return nil, err
	}
	return &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: key.Namespace,
		},
		Data: map[string]string{
			"data": base64.StdEncoding.EncodeToString(data),
		},
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DashboardReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&testgridv1alpha1.Dashboard{}).
		Named("dashboard").
		Complete(r)
}
