package exporter

import (
	"strings"
	"testing"
	"time"

	"github.com/cyb3er0x0x/dependency-tracker-exporterv5/internal/collector"
	"github.com/cyb3er0x0x/dependency-tracker-exporterv5/internal/dtrack"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

func testSnapshot() *collector.Snapshot {
	return &collector.Snapshot{
		CollectedAt: time.Unix(1700000000, 0),
		Portfolio: dtrack.Metrics{
			InheritedRiskScore: 42,
			Critical:           1, High: 2, Medium: 3, Low: 4, Unassigned: 5,
			FindingsAudited: 6, FindingsUnaudited: 7, FindingsTotal: 13,
			Suppressed: 8, Projects: 2, Components: 100,
			PolicyViolationsFail: 9, PolicyViolationsWarn: 10, PolicyViolationsInfo: 11,
			VulnerabilitiesKev: intPtr(3),
		},
		Projects: []dtrack.Project{
			{
				UUID: "11111111-1111-1111-1111-111111111111", Name: "app", Version: "1.0.0",
				Classifier: "APPLICATION", Active: true, IsLatest: boolPtr(true),
				Tags: []dtrack.Tag{{Name: "prod"}, {Name: "team-a"}}, LastBOMImport: 1699999999,
				Metrics: dtrack.Metrics{
					InheritedRiskScore: 12,
					Critical:           1, High: 0, Medium: 2, Low: 0, Unassigned: 1,
					FindingsAudited: 1, FindingsUnaudited: 3, FindingsTotal: 4,
					Suppressed: 2, Components: 50,
				},
			},
		},
		Violations: []dtrack.PolicyViolation{
			{
				Type:            "LICENSE",
				Project:         dtrack.ProjectRef{UUID: "11111111-1111-1111-1111-111111111111", Name: "app", Version: "1.0.0"},
				PolicyCondition: &dtrack.PolicyCondition{Policy: &dtrack.Policy{ViolationState: "WARN"}},
			},
		},
	}
}

// TestExporter_PreservedMetrics is the backward-compatibility guard: the
// upstream metric names and label sets must keep producing the same series.
func TestExporter_PreservedMetrics(t *testing.T) {
	store := collector.NewStore()
	store.Set(testSnapshot())
	e := New(store)

	const want = `
# HELP dependency_track_portfolio_inherited_risk_score The inherited risk score of the whole portfolio.
# TYPE dependency_track_portfolio_inherited_risk_score gauge
dependency_track_portfolio_inherited_risk_score 42
# HELP dependency_track_portfolio_vulnerabilities Number of vulnerabilities across the whole portfolio, by severity.
# TYPE dependency_track_portfolio_vulnerabilities gauge
dependency_track_portfolio_vulnerabilities{severity="CRITICAL"} 1
dependency_track_portfolio_vulnerabilities{severity="HIGH"} 2
dependency_track_portfolio_vulnerabilities{severity="LOW"} 4
dependency_track_portfolio_vulnerabilities{severity="MEDIUM"} 3
dependency_track_portfolio_vulnerabilities{severity="UNASSIGNED"} 5
# HELP dependency_track_portfolio_findings Number of findings across the whole portfolio, audited and unaudited.
# TYPE dependency_track_portfolio_findings gauge
dependency_track_portfolio_findings{audited="false"} 7
dependency_track_portfolio_findings{audited="true"} 6
# HELP dependency_track_project_info Project information.
# TYPE dependency_track_project_info gauge
dependency_track_project_info{active="true",classifier="APPLICATION",latest="true",name="app",tags=",prod,team-a,",uuid="11111111-1111-1111-1111-111111111111",version="1.0.0"} 1
# HELP dependency_track_project_vulnerabilities Number of vulnerabilities for a project by severity.
# TYPE dependency_track_project_vulnerabilities gauge
dependency_track_project_vulnerabilities{name="app",severity="CRITICAL",uuid="11111111-1111-1111-1111-111111111111",version="1.0.0"} 1
dependency_track_project_vulnerabilities{name="app",severity="HIGH",uuid="11111111-1111-1111-1111-111111111111",version="1.0.0"} 0
dependency_track_project_vulnerabilities{name="app",severity="LOW",uuid="11111111-1111-1111-1111-111111111111",version="1.0.0"} 0
dependency_track_project_vulnerabilities{name="app",severity="MEDIUM",uuid="11111111-1111-1111-1111-111111111111",version="1.0.0"} 2
dependency_track_project_vulnerabilities{name="app",severity="UNASSIGNED",uuid="11111111-1111-1111-1111-111111111111",version="1.0.0"} 1
# HELP dependency_track_project_last_bom_import Last BOM import date, represented as a Unix timestamp.
# TYPE dependency_track_project_last_bom_import gauge
dependency_track_project_last_bom_import{name="app",uuid="11111111-1111-1111-1111-111111111111",version="1.0.0"} 1699999999
# HELP dependency_track_project_inherited_risk_score Inherited risk score for a project.
# TYPE dependency_track_project_inherited_risk_score gauge
dependency_track_project_inherited_risk_score{name="app",uuid="11111111-1111-1111-1111-111111111111",version="1.0.0"} 12
`
	if err := testutil.CollectAndCompare(e, strings.NewReader(want),
		"dependency_track_portfolio_inherited_risk_score",
		"dependency_track_portfolio_vulnerabilities",
		"dependency_track_portfolio_findings",
		"dependency_track_project_info",
		"dependency_track_project_vulnerabilities",
		"dependency_track_project_last_bom_import",
		"dependency_track_project_inherited_risk_score",
	); err != nil {
		t.Fatal(err)
	}
}

func TestExporter_PolicyViolationSeriesInitialisedToZero(t *testing.T) {
	store := collector.NewStore()
	store.Set(testSnapshot())
	e := New(store)

	got := testutil.CollectAndCount(e, "dependency_track_project_policy_violations")
	// 3 types * 3 states * 4 analyses * 2 suppressed = 72 pre-initialised series.
	if got != 72 {
		t.Fatalf("policy_violations series count = %d, want 72", got)
	}
}

func TestExporter_NoSnapshot_NoMetrics(t *testing.T) {
	e := New(collector.NewStore())
	if n := testutil.CollectAndCount(e); n != 0 {
		t.Fatalf("expected 0 metrics with no snapshot, got %d", n)
	}
}
