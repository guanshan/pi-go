package tools

import "testing"

// TestGenerateUnifiedPatch checks the patch output is byte-identical to jsdiff v8
// createTwoFilesPatch (FILE_HEADERS_ONLY, 4 context lines), covering single-hunk
// replace, insert+delete in one hunk, no-final-newline, pure insert and delete.
func TestGenerateUnifiedPatch(t *testing.T) {
	cases := []struct {
		name     string
		old, new string
		want     string
	}{
		{
			name: "replace",
			old:  "line1\nline2\nline3\nline4\nline5\nline6\n",
			new:  "line1\nline2\nCHANGED\nline4\nline5\nline6\n",
			want: "--- f\n+++ f\n@@ -1,6 +1,6 @@\n line1\n line2\n-line3\n+CHANGED\n line4\n line5\n line6\n",
		},
		{
			name: "modify-and-add",
			old:  "a\nb\nc\n",
			new:  "a\nB\nc\nd\n",
			want: "--- f\n+++ f\n@@ -1,3 +1,4 @@\n a\n-b\n+B\n c\n+d\n",
		},
		{
			name: "no-final-newline",
			old:  "one\ntwo\nthree\n",
			new:  "one\ntwo\nthree",
			want: "--- f\n+++ f\n@@ -1,3 +1,3 @@\n one\n two\n-three\n+three\n\\ No newline at end of file\n",
		},
		{
			name: "pure-insert",
			old:  "x\ny\n",
			new:  "x\ny\nz\n",
			want: "--- f\n+++ f\n@@ -1,2 +1,3 @@\n x\n y\n+z\n",
		},
		{
			name: "pure-delete",
			old:  "keep\nremove me\nkeep2\n",
			new:  "keep\nkeep2\n",
			want: "--- f\n+++ f\n@@ -1,3 +1,2 @@\n keep\n-remove me\n keep2\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := generateUnifiedPatch("f", c.old, c.new, 4)
			if got != c.want {
				t.Fatalf("patch mismatch\n got: %q\nwant: %q", got, c.want)
			}
		})
	}
}
