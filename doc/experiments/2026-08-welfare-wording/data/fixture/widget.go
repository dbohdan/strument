package widget

// Round converts a measurement to a whole number of units.
func Round(x float64) int {
	return int(x + 0.5)
}
