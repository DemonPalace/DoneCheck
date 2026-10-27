package verify

const (
	PhaseMapped    = "mapped"
	PhaseScanning  = "scanning"
	PhaseAnalysing = "analysing"
	PhaseSyntax    = "syntax"
)

// Progress reports a long check's state; File is the file just finished, Files lists the mapped files
// (PhaseMapped) or the files about to be syntax-checked (PhaseSyntax).
type Progress struct {
	Check       string
	Phase       string
	File        string
	Files       []string
	Done, Total int
}
