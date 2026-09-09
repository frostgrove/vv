package i18n

import "testing"

func TestOptionalDistinguishesAbsentNullAndPresentValues(t *testing.T) {
	var absent Optional[int]
	if absent.Present() || absent.IsNull() {
		t.Fatal("zero Optional is not absent")
	}
	if value, ok := absent.Get(); ok || value != 0 {
		t.Fatalf("absent Get = %d, %v", value, ok)
	}
	present := Some(0)
	if !present.Present() || present.IsNull() {
		t.Fatal("present zero lost its presence")
	}
	if value, ok := present.Get(); !ok || value != 0 {
		t.Fatalf("present Get = %d, %v", value, ok)
	}
	null := NullValue[int]()
	if !null.Present() || !null.IsNull() {
		t.Fatal("explicit null lost its state")
	}
	if value, ok := null.Get(); ok || value != 0 {
		t.Fatalf("null Get = %d, %v", value, ok)
	}
}
