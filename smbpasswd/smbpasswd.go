// Package smbpasswd computes the values Samba's smbpasswd account table
// holds.
//
// Samba does not store a password. It stores the NT hash: the MD4 digest of
// the password encoded UTF-16 little-endian, which is what the SMB
// authentication exchange proves knowledge of. A configuration generator
// that wants an account table without putting a plaintext password in a
// deployed file computes the hash instead, which is what NTHash is for.
//
// MD4 is broken as a hash and the NT hash is unsalted: it is not a password
// hash and nothing here treats it as one. It is a protocol value, wanted
// because SMB wants exactly it, and knowing it is equivalent to knowing the
// password. A file holding one is as sensitive as the password it stands
// for, and is the reason a rendered account table is written 0600.
package smbpasswd

import (
	"encoding/hex"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/crypto/md4" //nolint:staticcheck // SMB's NT hash is defined as MD4; no other digest produces it.
)

// NoLMHash is the LM hash field of an entry that has none. Samba writes 32
// X characters there, and the LM hash is a broken relic no account should
// carry: a modern client never sends one, and an entry holding one would
// offer an attacker the weaker of two proofs for the same password.
const NoLMHash = "XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"

// AccountFlags is the flags field of an ordinary, enabled user account: `U`
// padded to the width Samba writes, inside brackets.
const AccountFlags = "[U          ]"

// ChangeTime is the last-change-time field every entry carries: a fixed
// moment, 2020-01-01T00:00:00Z. A rendered file is reproducible — the same
// inventory and the same secrets produce the same bytes — so the current
// time cannot go in it: every render would differ from the last, and a diff
// would stop saying whether anything changed.
//
// It must not be zero. Samba reads a password last set at time zero as one
// an administrator has flagged to be changed at next logon, whatever the
// account's other flags say, and refuses the logon with "password must
// change" — which a client cannot satisfy, since the password is this
// generator's to set. Any fixed moment in the past avoids that.
const ChangeTime = "LCT-5E0BE100"

// NTHash is the NT hash of password, as the 32 lowercase hexadecimal
// characters an smbpasswd entry holds.
//
// The password is encoded UTF-16 little-endian first, code unit by code
// unit, so a password outside ASCII hashes to what Windows and Samba
// compute for it rather than to a digest of its UTF-8 bytes.
func NTHash(password string) string {
	units := utf16.Encode([]rune(password))
	buf := make([]byte, 0, len(units)*2)
	for _, u := range units {
		buf = append(buf, byte(u), byte(u>>8))
	}
	sum := md4.New()
	sum.Write(buf)
	return hex.EncodeToString(sum.Sum(nil))
}

// Entry is one line of an smbpasswd file, without its newline:
//
//	name:uid:LM:NT:[flags]:LCT-xxxxxxxx:
//
// The LM hash is always absent, the flags are always an ordinary enabled
// account, and the change time is always ChangeTime, because those are the
// only values this generator has any business writing. The uid must be the one
// the account has where Samba runs: the entry names a POSIX account, and an
// entry whose uid belongs to nobody is one smbd refuses to authenticate.
func Entry(name string, uid int, password string) string {
	return strings.Join([]string{
		name,
		strconv.Itoa(uid),
		NoLMHash,
		strings.ToUpper(NTHash(password)),
		AccountFlags,
		ChangeTime,
		"",
	}, ":")
}
