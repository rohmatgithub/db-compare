package engine

import (
	"fmt"
	"math/big"
	"strings"
)

// KeyKind decides how key values are ordered while merging two row streams.
// The database must order rows the same way (see Dialect.SortExpr).
type KeyKind int

const (
	// KeyBytes orders values by their raw bytes.
	KeyBytes KeyKind = iota
	KeyInt
	KeyDecimal
)

func compareKey(kinds []KeyKind, a, b []string) (int, error) {
	for i, kind := range kinds {
		c, err := compareKeyPart(kind, a[i], b[i])
		if err != nil {
			return 0, err
		}
		if c != 0 {
			return c, nil
		}
	}
	return 0, nil
}

func compareKeyPart(kind KeyKind, a, b string) (int, error) {
	switch kind {
	case KeyInt:
		x, okA := new(big.Int).SetString(a, 10)
		y, okB := new(big.Int).SetString(b, 10)
		if !okA || !okB {
			return 0, fmt.Errorf("invalid integer key value %q or %q", a, b)
		}
		return x.Cmp(y), nil
	case KeyDecimal:
		x, okA := new(big.Rat).SetString(a)
		y, okB := new(big.Rat).SetString(b)
		if !okA || !okB {
			return 0, fmt.Errorf("invalid numeric key value %q or %q", a, b)
		}
		return x.Cmp(y), nil
	default:
		return strings.Compare(a, b), nil
	}
}

// intKeyArg converts an integer key to int64 or uint64 so that drivers bind
// it without a lossy float conversion.
func intKeyArg(value string) (any, error) {
	n, ok := new(big.Int).SetString(value, 10)
	switch {
	case !ok:
		return nil, fmt.Errorf("invalid integer key value %q", value)
	case n.IsInt64():
		return n.Int64(), nil
	case n.IsUint64():
		return n.Uint64(), nil
	default:
		return nil, fmt.Errorf("integer key value %q is out of range", value)
	}
}
