package neo4j

import "testing"

func TestBoolProp(t *testing.T) {
	t.Parallel()
	if got, err := boolProp(map[string]any{}, "x"); err != nil || got {
		t.Errorf("absent = (%v, %v), want (false, nil)", got, err)
	}
	if got, err := boolProp(map[string]any{"x": nil}, "x"); err != nil || got {
		t.Errorf("nil (projected absent) = (%v, %v), want (false, nil)", got, err)
	}
	if got, err := boolProp(map[string]any{"x": true}, "x"); err != nil || !got {
		t.Errorf("true = (%v, %v), want (true, nil)", got, err)
	}
	if _, err := boolProp(map[string]any{"x": "nope"}, "x"); err == nil {
		t.Error("a present non-bool value should surface an error")
	}
}
