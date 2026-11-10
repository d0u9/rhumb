package ntlm_test

import (
	"testing"

	"github.com/d0u9/rhumb/ntlm"
)

// TestNTHash pins the digest against values every other NT hash
// implementation produces for the same input, so a change to the encoding
// step — UTF-16 little-endian, not UTF-8 — is caught here rather than by a
// client that cannot log in.
func TestNTHash(t *testing.T) {
	cases := []struct {
		password string
		want     string
	}{
		{"", "31D6CFE0D16AE931B73C59D7E0C089C0"},
		{"password", "8846F7EAEE8FB117AD06BDD830B7586C"},
		{"Password1", "64F12CDDAA88057E06A81B54E73B949B"},
	}
	for _, c := range cases {
		if got := ntlm.NTHash(c.password); got != c.want {
			t.Errorf("NTHash(%q) = %q, want %q", c.password, got, c.want)
		}
	}
}

// TestNTHashIsUTF16 checks a password outside ASCII against the digest of
// its UTF-16 encoding rather than its UTF-8 bytes. Hashing the UTF-8 bytes
// is the mistake this exists to catch: it produces a file that renders
// cleanly and authenticates nobody.
func TestNTHashIsUTF16(t *testing.T) {
	// "密码" is U+5BC6 U+7801: four bytes as UTF-16LE, six as UTF-8.
	got := ntlm.NTHash("密码")
	utf8Digest := ntlm.NTHash(string([]rune{0xe5, 0xaf, 0x86, 0xe7, 0xa0, 0x81}))
	if got == utf8Digest {
		t.Fatal("NTHash hashed the UTF-8 bytes, not the UTF-16 code units")
	}
	if len(got) != 32 {
		t.Fatalf("NTHash returned %d characters, want 32", len(got))
	}
}

// TestNTHashIsStable checks that one password always hashes to one value.
// The NT hash is unsalted, which is a weakness of the protocol and a
// property a rendered file depends on: the same inventory renders the same
// bytes twice.
func TestNTHashIsStable(t *testing.T) {
	if ntlm.NTHash("hunter2") != ntlm.NTHash("hunter2") {
		t.Fatal("NTHash is not deterministic")
	}
}
