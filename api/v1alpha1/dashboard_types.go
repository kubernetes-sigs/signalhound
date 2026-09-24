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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	PASSING_STATUS = "PASSING"
	FAILING_STATUS = "FAILING"
	FLAKY_STATUS   = "FLAKY"
)

var ERROR_STATUSES = []string{FAILING_STATUS, FLAKY_STATUS}

// DashboardSpec defines the desired state of Dashboard.
type DashboardSpec struct {
	// DashboardTab is the name of the tab be scrapped from this board
	DashboardTab string `json:"dashboardTab,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=2
	// MinFailures is the minimum number of failures to consider a test group as failing
	MinFailures int `json:"minFailures,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=3
	// MinFlake is the minimum number of flakes to consider a test group as flaky
	MinFlakes int `json:"minFlakes,omitempty"`
}

// DashboardStatus defines the observed state of a testgrid Dashboard.
type DashboardStatus struct {
	// LastUpdate is the last fetched timestamp from testgrid.
	LastUpdate metav1.Time `json:"lastFetched,omitempty"`

	// DashboardSummary represents the list of Tabs summarized from a dashboard set in the spec.DashboardTab
	DashboardSummary []DashboardSummary `json:"summary,omitempty"`
}

// DashboardSummary represents summary information from a TestGrid dashboard
type DashboardSummary struct {
	LastRunTime    int64         `json:"last_run_timestamp,omitempty"`
	LastUpdateTime int64         `json:"last_update_timestamp,omitempty"`
	LastGreenRun   string        `json:"latest_green,omitempty"`
	OverallState   string        `json:"overall_status,omitempty"`
	CurrentState   string        `json:"status,omitempty"`
	DashboardName  string        `json:"dashboard_name,omitempty"`
	DashboardURL   string        `json:"url,omitempty"`
	DashboardTab   *DashboardTab `json:"dashboard_tab,omitempty"`
}

// DashboardTab represents test results for a specific dashboard tab
type DashboardTab struct {
	TabName   string       `json:"tab_name,omitempty"`
	TabURL    string       `json:"tab_url,omitempty"`
	BoardHash string       `json:"board_hash"`
	StateIcon string       `json:"icon"`
	TabState  string       `json:"state"`
	TestRuns  []TestResult `json:"tab_tests,omitempty"`
}

// TestResult contains details about an individual test run
type TestResult struct {
	TestName        string `json:"test_name"`
	LatestTimestamp int64  `json:"latest_timestamp"`
	FirstTimestamp  int64  `json:"first_timestamp"`
	TriageURL       string `json:"triage_url"`
	ProwJobURL      string `json:"prow_url"`
	ErrorMessage    string `json:"error_message"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Dashboard is the Schema for the dashboards API.
type Dashboard struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DashboardSpec   `json:"spec,omitempty"`
	Status DashboardStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DashboardList contains a list of Dashboard.
type DashboardList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Dashboard `json:"items"`
}
