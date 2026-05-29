// Package gitignore implements a small subset of git's ignore matching, shared
// by skill discovery and the find/grep tools so both honour .gitignore/.ignore
// rules consistently. It supports comments, negation (!), directory-only rules
// (trailing /), anchored vs basename patterns, and glob wildcards. Nested ignore
// files are modeled by prefixing their patterns with the directory they live in
// (see PrefixPattern). It is a leaf package with no pi dependencies.
package gitignore
