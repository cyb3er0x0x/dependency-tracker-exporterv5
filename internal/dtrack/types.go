package dtrack

// This package implements a thin Dependency-Track API client tailored to the
// exporter's needs. It deliberately does not depend on DependencyTrack/client-go
// because that library hard-codes a page size of 50 and has no support for the
// v2 REST API or token-based pagination.

// Tag is a project tag.
type Tag struct {
	Name string `json:"name"`
}

// Metrics mirrors the metrics object embedded in projects and returned by the
// portfolio/project metrics endpoints. Fields absent from a given
// Dependency-Track version simply decode as their zero value.
type Metrics struct {
	InheritedRiskScore float64 `json:"inheritedRiskScore"`
	Vulnerabilities    int     `json:"vulnerabilities"`
	Components         int     `json:"components"`
	Projects           int     `json:"projects"`
	Suppressed         int     `json:"suppressed"`

	Critical   int `json:"critical"`
	High       int `json:"high"`
	Medium     int `json:"medium"`
	Low        int `json:"low"`
	Unassigned int `json:"unassigned"`

	// KEV counts. Present on some Dependency-Track builds; when absent the
	// exporter derives the value from findings where possible.
	VulnerabilitiesKev *int `json:"vulnerabilitiesKev,omitempty"`

	VulnerableProjects   int `json:"vulnerableProjects"`
	VulnerableComponents int `json:"vulnerableComponents"`

	FindingsTotal     int `json:"findingsTotal"`
	FindingsAudited   int `json:"findingsAudited"`
	FindingsUnaudited int `json:"findingsUnaudited"`

	PolicyViolationsTotal     int `json:"policyViolationsTotal"`
	PolicyViolationsFail      int `json:"policyViolationsFail"`
	PolicyViolationsWarn      int `json:"policyViolationsWarn"`
	PolicyViolationsInfo      int `json:"policyViolationsInfo"`
	PolicyViolationsAudited   int `json:"policyViolationsAudited"`
	PolicyViolationsUnaudited int `json:"policyViolationsUnaudited"`

	PolicyViolationsSecurityTotal        int `json:"policyViolationsSecurityTotal"`
	PolicyViolationsSecurityAudited      int `json:"policyViolationsSecurityAudited"`
	PolicyViolationsSecurityUnaudited    int `json:"policyViolationsSecurityUnaudited"`
	PolicyViolationsLicenseTotal         int `json:"policyViolationsLicenseTotal"`
	PolicyViolationsLicenseAudited       int `json:"policyViolationsLicenseAudited"`
	PolicyViolationsLicenseUnaudited     int `json:"policyViolationsLicenseUnaudited"`
	PolicyViolationsOperationalTotal     int `json:"policyViolationsOperationalTotal"`
	PolicyViolationsOperationalAudited   int `json:"policyViolationsOperationalAudited"`
	PolicyViolationsOperationalUnaudited int `json:"policyViolationsOperationalUnaudited"`
}

// Project is a Dependency-Track project with its embedded latest metrics.
type Project struct {
	UUID          string  `json:"uuid"`
	Name          string  `json:"name"`
	Version       string  `json:"version"`
	Classifier    string  `json:"classifier"`
	Active        bool    `json:"active"`
	IsLatest      *bool   `json:"isLatest,omitempty"` // Since DT v4.12
	Tags          []Tag   `json:"tags,omitempty"`
	LastBOMImport int64   `json:"lastBomImport"`
	Metrics       Metrics `json:"metrics"`
}

// LatestState reports whether the project is flagged as the latest version.
// The second return value is false when the running Dependency-Track version
// does not expose the field.
func (p Project) LatestState() (isLatest bool, known bool) {
	if p.IsLatest == nil {
		return false, false
	}
	return *p.IsLatest, true
}

// ProjectRef is the trimmed project reference embedded in a policy violation.
type ProjectRef struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// PolicyViolation is a single policy violation.
type PolicyViolation struct {
	UUID            string             `json:"uuid"`
	Type            string             `json:"type"` // LICENSE | OPERATIONAL | SECURITY
	Project         ProjectRef         `json:"project"`
	PolicyCondition *PolicyCondition   `json:"policyCondition,omitempty"`
	Analysis        *ViolationAnalysis `json:"analysis,omitempty"`
}

// PolicyCondition carries the parent policy and its violation state.
type PolicyCondition struct {
	Policy *Policy `json:"policy,omitempty"`
}

// Policy is the parent policy of a condition.
type Policy struct {
	Name           string `json:"name"`
	ViolationState string `json:"violationState"` // INFO | WARN | FAIL
}

// ViolationAnalysis is the audit state of a policy violation.
type ViolationAnalysis struct {
	State      string `json:"analysisState"`
	Suppressed bool   `json:"isSuppressed"`
}

// State returns the violation state (INFO/WARN/FAIL) or "" when unavailable.
func (v PolicyViolation) State() string {
	if v.PolicyCondition != nil && v.PolicyCondition.Policy != nil {
		return v.PolicyCondition.Policy.ViolationState
	}
	return ""
}

// AnalysisState returns the audit analysis state and suppressed flag.
func (v PolicyViolation) AnalysisState() (state string, suppressed bool) {
	if v.Analysis == nil {
		return "", false
	}
	return v.Analysis.State, v.Analysis.Suppressed
}
