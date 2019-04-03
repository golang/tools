package source

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"log"
	"strings"

	doc "golang.org/x/tools/internal/lsp/godocmd"
)

const markupFormat = "```%s\n%s\n```"

type MarkedString markedString

type markedString struct {
	Language string `json:"language"`
	Value    string `json:"value"`

	isRawString bool
}

func (m *MarkedString) UnmarshalJSON(data []byte) error {
	if d := strings.TrimSpace(string(data)); len(d) > 0 && d[0] == '"' {
		// Raw string
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		m.Value = s
		m.isRawString = true
		return nil
	}
	// Language string
	ms := (*markedString)(m)
	return json.Unmarshal(data, ms)
}

func (m MarkedString) MarshalJSON() ([]byte, error) {
	if m.isRawString {
		return json.Marshal(m.Value)
	}
	return json.Marshal((markedString)(m))
}

func (m MarkedString) String() string {
	if m.isRawString {
		return m.Value
	}
	return fmt.Sprintf(markupFormat, m.Language, m.Value)
}

// RawMarkedString returns a MarkedString consisting of only a raw
// string (i.e., "foo" instead of {"value":"foo", "language":"bar"}).
func RawMarkedString(s string) MarkedString {
	return MarkedString{Value: s, isRawString: true}
}

func packageStatement(pkg Package, ident *ast.Ident) []MarkedString {
	comments := packageDoc(pkg.GetSyntax(), ident.Name)
	if pkgName := packageStatementName(pkg.GetSyntax(), ident); pkgName != "" {
		return maybeAddComments(comments, []MarkedString{{Language: "go", Value: "package " + pkgName}})
	}

	return nil
}

// packageStatementName returns the package name ((*ast.Ident).Name)
// of node iff node is the package statement of a file ("package p").
func packageStatementName(files []*ast.File, node *ast.Ident) string {
	for _, f := range files {
		if f.Name == node {
			return node.Name
		}
	}
	return ""
}

// maybeAddComments appends the specified comments converted to Markdown godoc
// form to the specified contents slice, if the comments string is not empty.
func maybeAddComments(comments string, contents []MarkedString) []MarkedString {
	if comments == "" {
		return contents
	}
	var b bytes.Buffer
	doc.ToMarkdown(&b, comments, nil)
	return append(contents, RawMarkedString(b.String()))
}

// formatNode format ast.Node
func formatNode(fset *token.FileSet, node ast.Node) string {
	buf := &bytes.Buffer{}
	if err := format.Node(buf, fset, node); err != nil {
		log.Println(err)
		return ""
	}
	return buf.String()
}

// prettyPrintTypesString is pretty printing specific to the output of
// types.*String. Instead of re-implementing the printer, we can just
// transform its output.
func prettyPrintTypesString(s string) string {
	// Don't bother including the fields if it is empty
	if strings.HasSuffix(s, "{}") {
		return ""
	}
	var b bytes.Buffer
	b.Grow(len(s))
	depth := 0
	var inTag bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case ';':
			if inTag {
				b.WriteByte(c)
				continue
			}

			b.WriteByte('\n')
			for j := 0; j < depth; j++ {
				b.WriteString("    ")
			}
			// Skip following space
			i++

		case '"':
			inTag = !inTag
			b.WriteByte('`')

		case '\\':
			b.WriteByte('"')
			//skip following "
			i++

		case '{':
			if i == len(s)-1 {
				// This should never happen, but in case it
				// does give up
				return s
			}

			n := s[i+1]
			if n == '}' {
				// Do not modify {}
				b.WriteString("{}")
				// We have already written }, so skip
				i++
			} else {
				// We expect fields to follow, insert a newline and space
				depth++
				b.WriteString(" {\n")
				for j := 0; j < depth; j++ {
					b.WriteString("    ")
				}
			}

		case '}':
			depth--
			if depth < 0 {
				return s
			}
			b.WriteString("\n}")

		default:
			b.WriteByte(c)
		}
	}

	return b.String()
}
