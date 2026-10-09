package domain

type ClipPage struct {
	Clips     []Clip
	Cursor    string
	Count     int
	LastViews int
}
