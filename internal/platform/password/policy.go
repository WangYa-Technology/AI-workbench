// Package password defines the policy for newly registered or reset passwords.
package password

import "unicode/utf8"

const MaxBytes = 72 // bcrypt rejects longer inputs; never silently truncate.

func ValidNew(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) >= 10 && len(value) <= MaxBytes
}
