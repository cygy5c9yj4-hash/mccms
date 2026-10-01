package mc

import "strconv"

// formatFloat 无多余尾零的浮点转字符串。
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', 4, 64)
}
