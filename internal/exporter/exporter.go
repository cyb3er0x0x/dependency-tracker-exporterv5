// Package exporter renders a cached Dependency-Track portfolio Snapshot as
// Prometheus metrics. It never talks to Dependency-Track itself; the background
// collector does that. Collect() is therefore fast and always succeeds.
package exporter

import (
	"strconv"
	"strings"

	"github.com/cyb3er0x0x/dependency-tracker-exporterv5/internal/collector"
	"github.com/cyb3er0x0x/dependency-tracker-exporterv5/internal/dtrack"
	"github.com/prometheus/client_golang/prometheus"
)

// Namespace is the metrics namespace of the exporter.
const Namespace = "dependency_track"

var (
	severities       = []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "UNASSIGNED"}
	violationTypes   = []string{"LICENSE", "OPERATIONAL", "SECURITY"}
	violationStates  = []string{"INFO", "WARN", "FAIL"}
	analysisStates   = []string{"APPROVED", "REJECTED", "NOT_SET", ""}
	suppressedStates = []string{"true", "false"}
)

// Exporter is a prometheus.Collector that renders the Store's current Snapshot.
type Exporter struct {
	store *collector.Store
}

// New returns an Exporter reading from store.
func New(store *collector.Store) *Exporter { return &Exporter{store: store} }

// Describe implements prometheus.Collector. The exporter emits a dynamic set of
// series, so it sends no descriptors (unchecked collector).
func (e *Exporter) Describe(chan<- *prometheus.Desc) {}

// Collect implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	snap := e.store.Get()
	if snap == nil {
		return
	}

	m := newMetrics()
	m.renderPortfolio(snap.Portfolio)
	for i := range snap.Projects {
		m.renderProject(&snap.Projects[i])
	}
	m.renderViolations(snap.Violations)
	m.collectAll(ch)
}

// --- metric set -------------------------------------------------------------

type metrics struct {
	// portfolio
	pInheritedRisk  prometheus.Gauge
	pVulns          *prometheus.GaugeVec // severity
	pFindings       *prometheus.GaugeVec // audited
	pFindingsTotal  prometheus.Gauge
	pSuppressed     prometheus.Gauge
	pProjects       prometheus.Gauge
	pComponents     prometheus.Gauge
	pVulnProjects   prometheus.Gauge
	pVulnComponents prometheus.Gauge
	pKev            prometheus.Gauge
	pPolicyViol     *prometheus.GaugeVec // state
	pPolicyByClass  *prometheus.GaugeVec // class, audited

	// project
	info          *prometheus.GaugeVec
	vulns         *prometheus.GaugeVec // uuid,name,version,severity
	policyViol    *prometheus.GaugeVec // uuid,name,version,type,state,analysis,suppressed
	policyViolTot *prometheus.GaugeVec // uuid,name,version,type,state
	lastBOMImport *prometheus.GaugeVec
	inheritedRisk *prometheus.GaugeVec
	findings      *prometheus.GaugeVec // uuid,name,version,audited
	findingsTotal *prometheus.GaugeVec
	suppressed    *prometheus.GaugeVec
	components    *prometheus.GaugeVec
	kev           *prometheus.GaugeVec
}

func fq(sub, name string) string { return prometheus.BuildFQName(Namespace, sub, name) }

func newMetrics() *metrics {
	projLabels := []string{"uuid", "name", "version"}
	return &metrics{
		pInheritedRisk:  prometheus.NewGauge(prometheus.GaugeOpts{Name: fq("portfolio", "inherited_risk_score"), Help: "The inherited risk score of the whole portfolio."}),
		pVulns:          prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("portfolio", "vulnerabilities"), Help: "Number of vulnerabilities across the whole portfolio, by severity."}, []string{"severity"}),
		pFindings:       prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("portfolio", "findings"), Help: "Number of findings across the whole portfolio, audited and unaudited."}, []string{"audited"}),
		pFindingsTotal:  prometheus.NewGauge(prometheus.GaugeOpts{Name: fq("portfolio", "findings_total"), Help: "Total number of findings across the whole portfolio."}),
		pSuppressed:     prometheus.NewGauge(prometheus.GaugeOpts{Name: fq("portfolio", "suppressed"), Help: "Number of suppressed vulnerabilities across the whole portfolio."}),
		pProjects:       prometheus.NewGauge(prometheus.GaugeOpts{Name: fq("portfolio", "projects"), Help: "Number of projects in the portfolio."}),
		pComponents:     prometheus.NewGauge(prometheus.GaugeOpts{Name: fq("portfolio", "components"), Help: "Number of components across the whole portfolio."}),
		pVulnProjects:   prometheus.NewGauge(prometheus.GaugeOpts{Name: fq("portfolio", "vulnerable_projects"), Help: "Number of projects with at least one vulnerability."}),
		pVulnComponents: prometheus.NewGauge(prometheus.GaugeOpts{Name: fq("portfolio", "vulnerable_components"), Help: "Number of components with at least one vulnerability."}),
		pKev:            prometheus.NewGauge(prometheus.GaugeOpts{Name: fq("portfolio", "kev"), Help: "Number of Known Exploited Vulnerabilities (CISA KEV) across the whole portfolio."}),
		pPolicyViol:     prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("portfolio", "policy_violations"), Help: "Number of policy violations across the whole portfolio, by state."}, []string{"state"}),
		pPolicyByClass:  prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("portfolio", "policy_violations_by_class"), Help: "Number of policy violations across the whole portfolio, by class and audit state."}, []string{"class", "audited"}),

		info:          prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("project", "info"), Help: "Project information."}, []string{"uuid", "name", "version", "classifier", "active", "latest", "tags"}),
		vulns:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("project", "vulnerabilities"), Help: "Number of vulnerabilities for a project by severity."}, []string{"uuid", "name", "version", "severity"}),
		policyViol:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("project", "policy_violations"), Help: "Policy violations for a project."}, []string{"uuid", "name", "version", "type", "state", "analysis", "suppressed"}),
		policyViolTot: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("project", "policy_violations_total"), Help: "Policy violation counts for a project as reported by project metrics."}, []string{"uuid", "name", "version", "type", "state"}),
		lastBOMImport: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("project", "last_bom_import"), Help: "Last BOM import date, represented as a Unix timestamp."}, projLabels),
		inheritedRisk: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("project", "inherited_risk_score"), Help: "Inherited risk score for a project."}, projLabels),
		findings:      prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("project", "findings"), Help: "Number of findings for a project, audited and unaudited."}, []string{"uuid", "name", "version", "audited"}),
		findingsTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("project", "findings_total"), Help: "Total number of findings for a project."}, projLabels),
		suppressed:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("project", "suppressed"), Help: "Number of suppressed vulnerabilities for a project."}, projLabels),
		components:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("project", "components"), Help: "Number of components for a project."}, projLabels),
		kev:           prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: fq("project", "kev"), Help: "Number of Known Exploited Vulnerabilities (CISA KEV) for a project."}, projLabels),
	}
}

func (m *metrics) collectAll(ch chan<- prometheus.Metric) {
	for _, c := range []prometheus.Collector{
		m.pInheritedRisk, m.pVulns, m.pFindings, m.pFindingsTotal, m.pSuppressed,
		m.pProjects, m.pComponents, m.pVulnProjects, m.pVulnComponents, m.pKev,
		m.pPolicyViol, m.pPolicyByClass,
		m.info, m.vulns, m.policyViol, m.policyViolTot, m.lastBOMImport,
		m.inheritedRisk, m.findings, m.findingsTotal, m.suppressed, m.components, m.kev,
	} {
		c.Collect(ch)
	}
}

func (m *metrics) renderPortfolio(p dtrack.Metrics) {
	m.pInheritedRisk.Set(p.InheritedRiskScore)
	sevVals := map[string]int{"CRITICAL": p.Critical, "HIGH": p.High, "MEDIUM": p.Medium, "LOW": p.Low, "UNASSIGNED": p.Unassigned}
	for _, s := range severities {
		m.pVulns.WithLabelValues(s).Set(float64(sevVals[s]))
	}
	m.pFindings.WithLabelValues("true").Set(float64(p.FindingsAudited))
	m.pFindings.WithLabelValues("false").Set(float64(p.FindingsUnaudited))
	m.pFindingsTotal.Set(float64(p.FindingsTotal))
	m.pSuppressed.Set(float64(p.Suppressed))
	m.pProjects.Set(float64(p.Projects))
	m.pComponents.Set(float64(p.Components))
	m.pVulnProjects.Set(float64(p.VulnerableProjects))
	m.pVulnComponents.Set(float64(p.VulnerableComponents))
	if p.VulnerabilitiesKev != nil {
		m.pKev.Set(float64(*p.VulnerabilitiesKev))
	}
	m.pPolicyViol.WithLabelValues("FAIL").Set(float64(p.PolicyViolationsFail))
	m.pPolicyViol.WithLabelValues("WARN").Set(float64(p.PolicyViolationsWarn))
	m.pPolicyViol.WithLabelValues("INFO").Set(float64(p.PolicyViolationsInfo))
	m.pPolicyByClass.WithLabelValues("SECURITY", "true").Set(float64(p.PolicyViolationsSecurityAudited))
	m.pPolicyByClass.WithLabelValues("SECURITY", "false").Set(float64(p.PolicyViolationsSecurityUnaudited))
	m.pPolicyByClass.WithLabelValues("LICENSE", "true").Set(float64(p.PolicyViolationsLicenseAudited))
	m.pPolicyByClass.WithLabelValues("LICENSE", "false").Set(float64(p.PolicyViolationsLicenseUnaudited))
	m.pPolicyByClass.WithLabelValues("OPERATIONAL", "true").Set(float64(p.PolicyViolationsOperationalAudited))
	m.pPolicyByClass.WithLabelValues("OPERATIONAL", "false").Set(float64(p.PolicyViolationsOperationalUnaudited))
}

func projTagsLabel(tags []dtrack.Tag) string {
	if len(tags) == 0 {
		return ","
	}
	var b strings.Builder
	b.WriteByte(',')
	for _, t := range tags {
		b.WriteString(t.Name)
		b.WriteByte(',')
	}
	return b.String()
}

func (m *metrics) renderProject(p *dtrack.Project) {
	uuid, name, ver := p.UUID, p.Name, p.Version

	latest := ""
	if v, known := p.LatestState(); known {
		latest = strconv.FormatBool(v)
	}
	m.info.With(prometheus.Labels{
		"uuid": uuid, "name": name, "version": ver,
		"classifier": p.Classifier,
		"active":     strconv.FormatBool(p.Active),
		"latest":     latest,
		"tags":       projTagsLabel(p.Tags),
	}).Set(1)

	pm := p.Metrics
	sevVals := map[string]int{"CRITICAL": pm.Critical, "HIGH": pm.High, "MEDIUM": pm.Medium, "LOW": pm.Low, "UNASSIGNED": pm.Unassigned}
	for _, s := range severities {
		m.vulns.WithLabelValues(uuid, name, ver, s).Set(float64(sevVals[s]))
	}
	m.lastBOMImport.WithLabelValues(uuid, name, ver).Set(float64(p.LastBOMImport))
	m.inheritedRisk.WithLabelValues(uuid, name, ver).Set(pm.InheritedRiskScore)
	m.findings.WithLabelValues(uuid, name, ver, "true").Set(float64(pm.FindingsAudited))
	m.findings.WithLabelValues(uuid, name, ver, "false").Set(float64(pm.FindingsUnaudited))
	m.findingsTotal.WithLabelValues(uuid, name, ver).Set(float64(pm.FindingsTotal))
	m.suppressed.WithLabelValues(uuid, name, ver).Set(float64(pm.Suppressed))
	m.components.WithLabelValues(uuid, name, ver).Set(float64(pm.Components))
	if pm.VulnerabilitiesKev != nil {
		m.kev.WithLabelValues(uuid, name, ver).Set(float64(*pm.VulnerabilitiesKev))
	}

	m.policyViolTot.WithLabelValues(uuid, name, ver, "SECURITY", "FAIL").Set(0)
	m.policyViolTot.WithLabelValues(uuid, name, ver, "ANY", "FAIL").Set(float64(pm.PolicyViolationsFail))
	m.policyViolTot.WithLabelValues(uuid, name, ver, "ANY", "WARN").Set(float64(pm.PolicyViolationsWarn))
	m.policyViolTot.WithLabelValues(uuid, name, ver, "ANY", "INFO").Set(float64(pm.PolicyViolationsInfo))
	m.policyViolTot.WithLabelValues(uuid, name, ver, "SECURITY", "ANY").Set(float64(pm.PolicyViolationsSecurityTotal))
	m.policyViolTot.WithLabelValues(uuid, name, ver, "LICENSE", "ANY").Set(float64(pm.PolicyViolationsLicenseTotal))
	m.policyViolTot.WithLabelValues(uuid, name, ver, "OPERATIONAL", "ANY").Set(float64(pm.PolicyViolationsOperationalTotal))

	// Pre-initialise every possible detailed violation series to 0 so that
	// counter-style increments record a 0 -> 1 transition. Mirrors upstream.
	for _, t := range violationTypes {
		for _, st := range violationStates {
			for _, an := range analysisStates {
				for _, su := range suppressedStates {
					m.policyViol.With(prometheus.Labels{
						"uuid": uuid, "name": name, "version": ver,
						"type": t, "state": st, "analysis": an, "suppressed": su,
					})
				}
			}
		}
	}
}

func (m *metrics) renderViolations(vs []dtrack.PolicyViolation) {
	for i := range vs {
		v := &vs[i]
		analysis, suppressed := v.AnalysisState()
		m.policyViol.With(prometheus.Labels{
			"uuid":       v.Project.UUID,
			"name":       v.Project.Name,
			"version":    v.Project.Version,
			"type":       v.Type,
			"state":      v.State(),
			"analysis":   analysis,
			"suppressed": strconv.FormatBool(suppressed),
		}).Inc()
	}
}
