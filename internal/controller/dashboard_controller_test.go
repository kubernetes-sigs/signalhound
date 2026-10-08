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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	testgridv1alpha1 "sigs.k8s.io/signalhound/api/v1alpha1"
	"sigs.k8s.io/signalhound/internal/testgrid"
)

var _ = Describe("Dashboard Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"
		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		tab := "sig-release-master-blocking"
		dashboard := &testgridv1alpha1.Dashboard{}
		BeforeEach(func() {
			By("creating the custom resource for the Kind Dashboard")
			err := k8sClient.Get(ctx, typeNamespacedName, dashboard)
			if err != nil && errors.IsNotFound(err) {
				resource := &testgridv1alpha1.Dashboard{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: testgridv1alpha1.DashboardSpec{
						DashboardTab: tab,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &testgridv1alpha1.Dashboard{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Dashboard")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &DashboardReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When filtering tests by the spec thresholds", func() {
		const resourceName = "threshold-resource"
		const dashboardName = "threshold-dashboard"

		DescribeTable("should apply minFailures to failing tabs and minFlakes to flaky tabs", func(minFailures, minFlakes int, wantFailing, wantFlaky float64) {
			By("serving a failing and a flaky tab whose only test failed twice")
			table := testgrid.TestGroup{
				Query:       "bucket/logs/ci-job",
				Timestamps:  []int64{3000, 2000, 1000},
				Changelists: []string{"3", "2", "1"},
				Tests: []testgrid.Test{{
					Name:       "failed-twice",
					ShortTexts: []string{"F", "F", ""},
					Messages:   []string{"failed", "failed", ""},
					Statuses:   []testgrid.Statuses{{Count: 2, Value: 12}, {Count: 1, Value: 1}}, // FAIL x2, PASS
				}},
			}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /"+dashboardName+"/summary", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(testgrid.DashboardMapper{
					"failing-tab": {OverallState: testgridv1alpha1.FAILING_STATUS, DashboardName: dashboardName},
					"flaky-tab":   {OverallState: testgridv1alpha1.FLAKY_STATUS, DashboardName: dashboardName},
				})
			})
			mux.HandleFunc("GET /"+dashboardName+"/table", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(table)
			})
			server := httptest.NewServer(mux)
			DeferCleanup(server.Close)
			originalURL := testgrid.URL
			testgrid.URL = server.URL
			DeferCleanup(func() { testgrid.URL = originalURL })

			By("creating a Dashboard with unequal thresholds")
			resource := &testgridv1alpha1.Dashboard{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: metav1.NamespaceDefault},
				Spec: testgridv1alpha1.DashboardSpec{
					DashboardTab: dashboardName,
					MinFailures:  minFailures,
					MinFlakes:    minFlakes,
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			})

			By("reconciling the created resource")
			Expect(initMetrics()).To(Succeed())
			controllerReconciler := &DashboardReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: resourceName, Namespace: metav1.NamespaceDefault},
			})
			Expect(err).NotTo(HaveOccurred())

			By("checking the tab gauges")
			Expect(tabGauge("testgrid_test_failures_total_ratio", dashboardName, "failing-tab")).To(Equal(wantFailing))
			Expect(tabGauge("testgrid_test_flakes_total_ratio", dashboardName, "flaky-tab")).To(Equal(wantFlaky))
		},
			Entry("when minFailures is the lower threshold", 2, 3, 1.0, 0.0),
			Entry("when minFlakes is the lower threshold", 3, 2, 0.0, 1.0),
		)
	})
})

// tabGauge returns the value a gauge exports for one TestGrid dashboard tab.
func tabGauge(name, dashboard, tab string) float64 {
	families, err := metrics.Registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, m := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range m.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["dashboard"] == dashboard && labels["tab"] == tab {
				return m.GetGauge().GetValue()
			}
		}
	}
	Fail(fmt.Sprintf("no %s sample for %s#%s", name, dashboard, tab))
	return 0
}
