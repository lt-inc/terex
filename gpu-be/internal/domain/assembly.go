package domain

type MediaProbe struct {
	DurationSec   float64
	Width, Height int
	HasAudio      bool
}
type AssemblySegment struct {
	File             string
	StartSec, EndSec float64
	NextChannel      string
}
