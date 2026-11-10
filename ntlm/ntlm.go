// Package ntlm computes the NT hash, the password-derived value the NTLM and
// SMB authentication exchanges prove knowledge of.
//
// The NT hash is the MD4 digest of the password encoded UTF-16
// little-endian. A configuration generator that wants an account file
// without putting a plaintext password in a deployed file computes the hash
// instead, which is what NTHash is for.
//
// MD4 is broken as a hash and the NT hash is unsalted: it is not a password
// hash and nothing here treats it as one. It is a protocol value, wanted
// because the protocol wants exactly it, and knowing it is equivalent to
// knowing the password. A file holding one is as sensitive as the password
// it stands for.
package ntlm

import (
	"encoding/hex"
	"strings"
	"unicode/utf16"

	"golang.org/x/crypto/md4" //nolint:staticcheck // The NT hash is defined as MD4; no other digest produces it.
)

// NTHash is the NT hash of password, as the 32 uppercase hexadecimal
// characters account files conventionally hold.
//
// The password is encoded UTF-16 little-endian first, code unit by code
// unit, so a password outside ASCII hashes to what Windows computes for it
// rather than to a digest of its UTF-8 bytes.
func NTHash(password string) string {
	units := utf16.Encode([]rune(password))
	buf := make([]byte, 0, len(units)*2)
	for _, u := range units {
		buf = append(buf, byte(u), byte(u>>8))
	}
	sum := md4.New()
	sum.Write(buf)
	return strings.ToUpper(hex.EncodeToString(sum.Sum(nil)))
}
