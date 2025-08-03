package util

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

func ConcatenateColumns(data map[string]any, columns []string) string {
	if data == nil || len(columns) == 0 {
		return ""
	}
	parts := make([]string, 0, len(columns))
	for _, c := range columns {
		if v, ok := data[c]; ok && v != nil {
			s := toString(v)
			if s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, " ")
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func NormalizeVector(v []float32) []float32 {
	sum := float64(0)
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	n := float32(math.Sqrt(sum))
	if n == 0 {
		return v
	}
	out := make([]float32, len(v))
	for i := range v {
		out[i] = v[i] / n
	}
	return out
}

func SortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
