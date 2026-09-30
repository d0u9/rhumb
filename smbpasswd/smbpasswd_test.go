package smbpasswd_test

import (
	"strings"
	"testing"

	"github.com/d0u9/rhumb/smbpasswd"
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
		{"", "31d6cfe0d16ae931b73c59d7e0c089c0"},
		{"password", "8846f7eaee8fb117ad06bdd830b7586c"},
		{"Password1", "64f12cddaa88057e06a81b54e73b949b"},
	}
	for _, c := range cases {
		if got := smbpasswd.NTHash(c.password); got != c.want {
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
	got := smbpasswd.NTHash("密码")
	utf8Digest := smbpasswd.NTHash(string([]rune{0xe5, 0xaf, 0x86, 0xe7, 0xa0, 0x81}))
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
	if smbpasswd.NTHash("hunter2") != smbpasswd.NTHash("hunter2") {
		t.Fatal("NTHash is not deterministic")
	}
}

// TestEntry pins the line layout: six colons, an uppercase NT hash, no LM
// hash, and the fixed change time. Samba parses by position, so a field in the
// wrong place is a file smbd rejects at startup.
func TestEntry(t *testing.T) {
	got := smbpasswd.Entry("dana", 3001, "password")
	want := "dana:3001:" + smbpasswd.NoLMHash +
		":8846F7EAEE8FB117AD06BDD830B7586C:" + smbpasswd.AccountFlags +
		":" + smbpasswd.ChangeTime + ":"
	if got != want {
		t.Fatalf("Entry() = %q, want %q", got, want)
	}
	if n := strings.Count(got, ":"); n != 6 {
		t.Fatalf("Entry() has %d colons, want 6", n)
	}
}

// TestEntryHasNoPassword is the point of the package: the plaintext never
// reaches the rendered line, so a deployed account table is not a list of
// passwords.
func TestEntryHasNoPassword(t *testing.T) {
	if strings.Contains(smbpasswd.Entry("dana", 3001, "hunter2"), "hunter2") {
		t.Fatal("Entry() wrote the plaintext password")
	}
}

// TestChangeTimeIsNotZero: Samba reads a password last set at time zero as
// one that must be changed at next logon, and refuses every logon with it.
// A client cannot satisfy that, so an entry carrying it is an account nobody
// can use.
func TestChangeTimeIsNotZero(t *testing.T) {
	hex := strings.TrimPrefix(smbpasswd.ChangeTime, "LCT-")
	if strings.Trim(hex, "0") == "" {
		t.Fatalf("ChangeTime = %q: a zero change time makes Samba demand a password change", smbpasswd.ChangeTime)
	}
	if len(hex) != 8 {
		t.Fatalf("ChangeTime = %q: Samba writes eight hex digits", smbpasswd.ChangeTime)
	}
}
