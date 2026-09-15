package bakpack

import (
	"testing"
)

func TestCanonicalJSONPreservesLargeIntegerText(t *testing.T) {
	for _, text := range []string{
		"18446744073709551617",  // beyond uint64 range
		"18446744073709551618",  // adjacent, must not collide with the above
		"-99999999999999999999", // beyond int64 range, negative
		"9223372036854775807",   // math.MaxInt64, still within int64
	} {
		value, err := DecodeJSON([]byte(text))
		if err != nil {
			t.Fatalf("DecodeJSON(%q) error = %v", text, err)
		}
		got, err := CanonicalJSON(value)
		if err != nil {
			t.Fatalf("CanonicalJSON(%q) error = %v", text, err)
		}
		if string(got) != text {
			t.Fatalf("CanonicalJSON(%q) = %q, want unchanged decimal text", text, got)
		}
	}
}

func TestCanonicalJSONDistinguishesLargeIntegersBeyondInt64(t *testing.T) {
	a, err := JSONBytesCanonicalSHA256([]byte("18446744073709551617"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := JSONBytesCanonicalSHA256([]byte("18446744073709551618"))
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("distinct large integers canonicalized to the same checksum %s", a)
	}
}

func TestCanonicalJSONStillNormalizesNonIntegerNumbers(t *testing.T) {
	value, err := DecodeJSON([]byte("1.50"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1.5" {
		t.Fatalf("CanonicalJSON(1.50) = %q, want normalized 1.5", got)
	}
}
