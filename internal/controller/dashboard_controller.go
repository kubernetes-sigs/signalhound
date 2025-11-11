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
	"reflect"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	testgridv1alpha1 "sigs.k8s.io/signalhound/api/v1alpha1"
	"sigs.k8s.io/signalhound/internal/testgrid"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const meterName = "signalhound"

// Metrics holds OpenTelemetry metric instruments
type Metrics struct {
	dashboardStateGauge metric.Int64Gauge
	tabStateGauge       metric.Int64Gauge
	lastRunTimestamp    metric.Int64Gauge
	lastUpdateTimestamp metric.Int64Gauge
	totalTestFailures   metric.Int64Gauge
	totalTestFlakes     metric.Int64Gauge
	testFailuresCounter metric.Int64Counter
}

// globalMetrics holds the initialized metrics
var globalMetrics *Metrics

func init() {
	exporter, err := prometheus.New(
		prometheus.WithRegisterer(metrics.Registry),
	)
	if err != nil {
		panic(err)
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
	)
	otel.SetMeterProvider(provider)
}

// initMetrics initializes OpenTelemetry metrics
func initMetrics() error {
	meter := otel.Meter(meterName)

	dashboardStateGauge, err := meter.Int64Gauge(
		"testgrid_dashboard_state",
		metric.WithDescription("Current state of testgrid dashboard (1 = active state)"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return err
	}

	tabStateGauge, err := meter.Int64Gauge(
		"testgrid_tab_state",
		metric.WithDescription("State of testgrid dashboard tab"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return err
	}

	lastRunTimestamp, err := meter.Int64Gauge(
		"testgrid_dashboard_last_run_timestamp",
		metric.WithDescription("Unix timestamp of the last test run for a dashboard tab"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return err
	}

	lastUpdateTimestamp, err := meter.Int64Gauge(
		"testgrid_dashboard_last_update_timestamp",
		metric.WithDescription("Unix timestamp of the last update for a dashboard tab"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return err
	}

	totalTestFailures, err := meter.Int64Gauge(
		"testgrid_test_failures_total",
		metric.WithDescription("Total number of failing tests in a dashboard tab"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return err
	}

	totalTestFlakes, err := meter.Int64Gauge(
		"testgrid_test_flakes_total",
		metric.WithDescription("Total number of flaky tests in a dashboard tab"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return err
	}

	testFailuresCounter, err := meter.Int64Counter(
		"testgrid_individual_test_failures_total",
		metric.WithDescription("Counter of failures for individual tests"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return err
	}

	globalMetrics = &Metrics{
		dashboardStateGauge: dashboardStateGauge,
		tabStateGauge:       tabStateGauge,
		lastRunTimestamp:    lastRunTimestamp,
		lastUpdateTimestamp: lastUpdateTimestamp,
		totalTestFailures:   totalTestFailures,
		totalTestFlakes:     totalTestFlakes,
		testFailuresCounter: testFailuresCounter,
	}

	return nil
}

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

	// Create a span for tracing
	tracer := otel.Tracer(meterName)
	ctx, span := tracer.Start(ctx, "DashboardReconcile")
	defer span.End()

	span.SetAttributes(
		attribute.String("dashboard.name", req.Name),
		attribute.String("dashboard.namespace", req.Namespace),
	)

	var dashboard testgridv1alpha1.Dashboard
	if err := r.Get(ctx, req.NamespacedName, &dashboard); err != nil {
		r.log.Error(err, "unable to fetch dashboard")
		span.RecordError(err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	grid := testgrid.NewTestGrid(testgrid.URL)
	dashboardSummaries, err := grid.FetchTabSummary(dashboard.Spec.DashboardTab, testgridv1alpha1.ERROR_STATUSES)
	if err != nil {
		r.log.Error(err, "error fetching summary from endpoint.")
		span.RecordError(err)
		return ctrl.Result{}, err
	}

	span.SetAttributes(attribute.Int("summaries.count", len(dashboardSummaries)))

	// set the dashboard summary on status if an update happened
	if r.shouldRefresh(dashboard.Status, dashboardSummaries) {
		dashboard.Status.DashboardSummary = dashboardSummaries
		dashboard.Status.LastUpdate = metav1.Now()

		r.log.Info("updating dashboard object status.")
		if err := r.Status().Update(ctx, &dashboard); err != nil {
			r.log.Error(err, "unable to update dashboard status")
			span.RecordError(err)
			return ctrl.Result{}, err
		}

		for _, dashSummary := range dashboardSummaries {
			tabName := dashSummary.DashboardTab.TabName

			var tab *testgridv1alpha1.DashboardTab
			if tab, err = grid.FetchTabTests(&dashSummary, dashboard.Spec.MinFlakes, dashboard.Spec.MinFailures); err != nil {
				r.log.Error(err, "error fetching table", "tab", tabName)
				span.RecordError(err)
				continue
			}

			// record metrics for this tab summary
			r.recordMetrics(ctx, &dashSummary, tab)
		}
	}

	r.log.V(1).Info("reconciliation completed successfully")
	span.SetAttributes(attribute.Bool("reconcile.success", true))

	return ctrl.Result{}, nil
}

// recordMetrics records OpenTelemetry metrics for testgrid dashboard failures and flakes
func (r *DashboardReconciler) recordMetrics(ctx context.Context, dashSummary *testgridv1alpha1.DashboardSummary, tab *testgridv1alpha1.DashboardTab) {
	if globalMetrics == nil {
		r.log.Error(nil, "metrics not initialized")
		return
	}

	dashboardName := dashSummary.DashboardName
	tabName := dashSummary.DashboardTab.TabName

	// common attributes for all metrics
	dashboardAttr := attribute.String("dashboard", dashboardName)
	tabAttr := attribute.String("tab", tabName)

	// record dashboard-level state metrics
	overallStateAttr := attribute.String("overall_state", dashSummary.OverallState)
	globalMetrics.dashboardStateGauge.Record(ctx, 1,
		metric.WithAttributes(dashboardAttr, tabAttr, overallStateAttr))

	currentStateAttr := attribute.String("state", dashSummary.CurrentState)
	globalMetrics.dashboardStateGauge.Record(ctx, 1,
		metric.WithAttributes(dashboardAttr, tabAttr, currentStateAttr))

	// record timestamp metrics
	if dashSummary.LastRunTime > 0 {
		globalMetrics.lastRunTimestamp.Record(ctx, dashSummary.LastRunTime,
			metric.WithAttributes(dashboardAttr, tabAttr))
	}
	if dashSummary.LastUpdateTime > 0 {
		globalMetrics.lastUpdateTimestamp.Record(ctx, dashSummary.LastUpdateTime,
			metric.WithAttributes(dashboardAttr, tabAttr))
	}

	// set metric for specific test
	for _, testResult := range tab.TestRuns {
		testNameAttr := attribute.String("test_name", testResult.TestName)
		tabState := attribute.String("tab_state", tab.TabState)
		globalMetrics.testFailuresCounter.Add(ctx, 1,
			metric.WithAttributes(dashboardAttr, tabAttr, testNameAttr, tabState))
	}

	// record aggregate counts based on tab state
	switch tab.TabState {
	case testgridv1alpha1.FAILING_STATUS:
		globalMetrics.totalTestFailures.Record(ctx, int64(len(tab.TestRuns)),
			metric.WithAttributes(dashboardAttr, tabAttr))
	case testgridv1alpha1.FLAKY_STATUS:
		globalMetrics.totalTestFlakes.Record(ctx, int64(len(tab.TestRuns)),
			metric.WithAttributes(dashboardAttr, tabAttr))
	}

	// record final tab state gauge
	tabStateAttr := attribute.String("state", tab.TabState)
	globalMetrics.tabStateGauge.Record(ctx, 1,
		metric.WithAttributes(dashboardAttr, tabAttr, tabStateAttr))

	r.log.V(1).Info("recorded metrics",
		"dashboard", dashboardName,
		"tab", tabName,
		"tab_state", tab.TabState,
		"tests", len(tab.TestRuns))
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

// SetupWithManager sets up the controller with the Manager.
func (r *DashboardReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := initMetrics(); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&testgridv1alpha1.Dashboard{}).
		Named("dashboard").
		Complete(r)
}
