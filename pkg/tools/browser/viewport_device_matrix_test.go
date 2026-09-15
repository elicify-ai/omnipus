package browser

func trimFloat(f float64) string {
	switch f {
	case 1:
		return "1"
	case 1.5:
		return "1.5"
	case 2:
		return "2"
	case 3:
		return "3"
	}
	return "x"
}
