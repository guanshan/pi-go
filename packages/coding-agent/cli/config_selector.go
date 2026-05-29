package cli

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func PromptConfigSelection(in io.Reader, out io.Writer, maxIndex int) ([]int, error) {
	fmt.Fprint(out, "\nToggle numbers (comma/space separated), or press Enter to exit: ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	return ParseConfigSelection(line, maxIndex)
}

func ParseConfigSelection(line string, maxIndex int) ([]int, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, nil
	}
	parts := strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	seen := map[int]bool{}
	var indexes []int
	for _, part := range parts {
		index, err := strconv.Atoi(part)
		if err != nil || index < 1 || index > maxIndex {
			return nil, fmt.Errorf("invalid resource number %q", part)
		}
		if !seen[index] {
			seen[index] = true
			indexes = append(indexes, index)
		}
	}
	return indexes, nil
}
