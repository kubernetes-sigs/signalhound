# Controller Metrics

The SignalHound controller exports Prometheus metrics for TestGrid dashboard monitoring. 
This document describes the available metrics and their usage.

## Overview

All testgrid dashboard data is now exposed as Prometheus metrics that can be scraped and visualized.

## Available Metrics

### Dashboard State Metrics

#### `testgrid_dashboard_state`

Tracks the current state of testgrid dashboards.

**Type:** Gauge
**Labels:**
- `dashboard`: Dashboard name (e.g., "sig-release-master-blocking")
- `tab`: Tab name within the dashboard
- `state`: Current state (PASSING, FAILING, FLAKY)

**Values:**
- `1` = active state
- `0` = inactive state

**Example:**
```promql
testgrid_dashboard_state{dashboard="sig-release-master-blocking",tab="gce-cos-master-default",state="FAILING"}
```

**Usage:**
```promql
# Count dashboards in FAILING state
count(testgrid_dashboard_state{state="FAILING"} == 1)

# Get all tabs in FLAKY state
testgrid_dashboard_state{state="FLAKY"} == 1
```

#### `testgrid_tab_state`

Tracks the state of individual dashboard tabs.

**Type:** Gauge
**Labels:**
- `dashboard`: Dashboard name
- `tab`: Tab name
- `state`: Tab state

**Example:**
```promql
testgrid_tab_state{dashboard="sig-release-master-blocking",tab="gce-cos-master-default",state="FAILING"}
```

### Timestamp Metrics

#### `testgrid_dashboard_last_run_timestamp`
Unix timestamp of the last test run for a dashboard tab.

**Type:** Gauge
**Labels:**
- `dashboard`: Dashboard name
- `tab`: Tab name

**Example:**
```promql
testgrid_dashboard_last_run_timestamp{dashboard="sig-release-master-blocking",tab="gce-cos-master-default"}
```

**Usage:**
```promql
# Time since last run (in seconds)
time() - testgrid_dashboard_last_run_timestamp

# Alert if no runs in last hour
time() - testgrid_dashboard_last_run_timestamp > 3600
```

#### `testgrid_dashboard_last_update_timestamp`

Unix timestamp of the last update for a dashboard tab.

**Type:** Gauge
**Labels:**
- `dashboard`: Dashboard name
- `tab`: Tab name

**Example:**
```promql
testgrid_dashboard_last_update_timestamp{dashboard="sig-release-master-blocking",tab="gce-cos-master-default"}
```

### Test Failure Metrics

#### `testgrid_test_failures_total`
Total number of failing tests in a dashboard tab.

**Type:** Gauge
**Labels:**
- `dashboard`: Dashboard name
- `tab`: Tab name

**Example:**
```promql
testgrid_test_failures_total{dashboard="sig-release-master-blocking",tab="gce-cos-master-default"}
```

**Usage:**
```promql
# Total failures across all tabs
sum(testgrid_test_failures_total)

# Failures by dashboard
sum by (dashboard) (testgrid_test_failures_total)

# Alert if failures > 10
testgrid_test_failures_total > 10
```

#### `testgrid_test_flakes_total`

Total number of flaky tests in a dashboard tab.

**Type:** Gauge
**Labels:**
- `dashboard`: Dashboard name
- `tab`: Tab name

**Example:**
```promql
testgrid_test_flakes_total{dashboard="sig-release-master-blocking",tab="gce-cos-master-default"}
```

#### `testgrid_individual_test_failures_total`

Counter of failures for individual tests.

**Type:** Counter
**Labels:**
- `dashboard`: Dashboard name
- `tab`: Tab name
- `test_name`: Individual test name

**Example:**
```promql
testgrid_individual_test_failures_total{dashboard="sig-release-master-blocking",tab="gce-cos-master-default",test_name="[sig-node] Pods should be submitted and removed"}
```

**Usage:**
```promql
# Most failing tests
topk(10, rate(testgrid_individual_test_failures_total[1h]))

# Failure rate per test
rate(testgrid_individual_test_failures_total[5m])

# Tests failing more than 5 times in last hour
testgrid_individual_test_failures_total - testgrid_individual_test_failures_total offset 1h > 5
```

## Metrics Endpoint

The controller exposes metrics on the standard controller-runtime metrics endpoint:

**Default endpoint:** `http://localhost:8080/metrics`

## Prometheus Configuration

Add the following to your Prometheus scrape configuration:

```yaml
scrape_configs:
  - job_name: 'signalhound-controller'
    static_configs:
      - targets: ['signalhound-controller-service:8080']
    metrics_path: /metrics
    scrape_interval: 60s
```

For Kubernetes deployments, use a ServiceMonitor:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: signalhound-controller
  namespace: signalhound-system
spec:
  selector:
    matchLabels:
      control-plane: controller-manager
  endpoints:
  - port: metrics
    path: /metrics
    interval: 60s
```

## Example Queries

### Dashboard Health

```promql
# Dashboards currently failing
count(testgrid_dashboard_state{state="FAILING"} == 1)

# Dashboards currently flaky
count(testgrid_dashboard_state{state="FLAKY"} == 1)

# All healthy dashboards
count(testgrid_dashboard_state{state="PASSING"} == 1)
```

### Test Failures

```promql
# Total test failures across all dashboards
sum(testgrid_test_failures_total)

# Failures by dashboard (top 5)
topk(5, sum by (dashboard) (testgrid_test_failures_total))

# Tabs with most failures
topk(10, testgrid_test_failures_total)
```

### Test Flakes

```promql
# Total flaky tests
sum(testgrid_test_flakes_total)

# Flake rate by dashboard
sum by (dashboard) (testgrid_test_flakes_total)
```

### Failure Trends

```promql
# Failure increase over last hour
sum(testgrid_test_failures_total) - sum(testgrid_test_failures_total offset 1h)

# Tests with increasing failure rate
increase(testgrid_individual_test_failures_total[1h]) > 0
```

### Time-based Queries

```promql
# Time since last successful run (seconds)
time() - testgrid_dashboard_last_run_timestamp

# Stale dashboards (not updated in 2 hours)
time() - testgrid_dashboard_last_update_timestamp > 7200
```

## Grafana Dashboard Examples

### Dashboard Overview Panel
```promql
# Single stat: Total failing dashboards
count(testgrid_dashboard_state{state="FAILING"} == 1)
```

### Failure Timeline Graph
```promql
# Graph: Test failures over time
sum(testgrid_test_failures_total)
```

### Top Failing Tests Table
```promql
# Table: Most common test failures
topk(20, rate(testgrid_individual_test_failures_total[6h]))
```

## Alert Examples

### Prometheus Alert Rules

```yaml
groups:
  - name: testgrid_alerts
    interval: 60s
    rules:
      - alert: HighTestFailureRate
        expr: sum(testgrid_test_failures_total) > 50
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "High test failure rate detected"
          description: "Total test failures: {{ $value }}"

      - alert: DashboardStale
        expr: time() - testgrid_dashboard_last_update_timestamp > 3600
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Dashboard {{ $labels.dashboard }}/{{ $labels.tab }} is stale"
          description: "No updates in over 1 hour"

      - alert: CriticalTestsFlaky
        expr: testgrid_test_flakes_total{dashboard="sig-release-master-blocking"} > 20
        for: 15m
        labels:
          severity: critical
        annotations:
          summary: "High flake rate in blocking dashboards"
          description: "{{ $value }} flaky tests in {{ $labels.tab }}"

      - alert: IndividualTestHighFailureRate
        expr: rate(testgrid_individual_test_failures_total[1h]) > 0.1
        for: 30m
        labels:
          severity: warning
        annotations:
          summary: "Test {{ $labels.test_name }} failing frequently"
          description: "Failure rate: {{ $value }} per second"
```

## Controller Configuration

The controller automatically registers metrics on startup. No additional configuration is required.

### Reconciliation Behavior

- Metrics are updated when dashboard data changes
- Minimum refresh interval: 1 minute (configurable via `shouldRefresh()`)
- Metrics persist until next update
- Counter metrics (`testgrid_individual_test_failures_total`) increment on each failure detection

## Best Practices

1. **Scrape interval**: Set to match your reconciliation frequency (recommended: 60s)
2. **Retention**: Configure Prometheus retention based on your analysis needs
3. **Cardinality**: Monitor metric cardinality if you have many tests/dashboards
4. **Alerting**: Start with critical dashboards only, expand alerts gradually
5. **Dashboards**: Create separate Grafana dashboards for different teams/SIGs

## Summary

The Prometheus metrics provide comprehensive visibility into TestGrid dashboard health, test failures, and flake rates. Use these metrics to:

- Monitor CI/CD health in real-time
- Detect and alert on test failures
- Track failure trends over time
- Identify problematic tests
- Generate reports for release teams
- Integrate with existing monitoring infrastructure
