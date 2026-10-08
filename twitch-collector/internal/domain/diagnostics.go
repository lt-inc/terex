package domain

type DiagnosticBudgetFlow struct {
	FlowID string `json:"flow_id"`
	Name   string `json:"name"`
	Format string `json:"format"`
	Spent  int    `json:"spent"`
}
type FlowDiagnostics struct {
	Available                bool                   `json:"available"`
	DailyLimit               int                    `json:"daily_limit"`
	Spent                    int                    `json:"spent"`
	Remaining                int                    `json:"remaining"`
	Timezone                 string                 `json:"timezone"`
	CompilationBudgetPercent int                    `json:"compilation_budget_percent"`
	AdmissionMode            string                 `json:"admission_mode"`
	ByFlow                   []DiagnosticBudgetFlow `json:"by_flow"`
	Clips                    []ClipStatus           `json:"clips"`
	Discovery                []DiscoveryReport      `json:"discovery"`
}
