package css

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type pos struct {
	line int
	col  int
}

func (p pos) String() string {
	if p.line == 0 && p.col == 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d:", p.line, p.col)
}

type token struct {
	pos   pos
	tok   Token
	sub   TypeFlag
	lit   string
	unit  string
	start uint32
	end   uint32
}

func (t token) String() string {
	if t.lit == "" && t.sub == TypeFlagNone && t.unit == "" && t.start == 0 && t.end == 0 {
		return fmt.Sprintf("%stok:%s", t.pos, t.tok)
	}
	if t.sub == TypeFlagNone && t.unit == "" && t.start == 0 && t.end == 0 {
		return fmt.Sprintf("{%s%s %q}", t.pos, t.tok, t.lit)
	}
	if t.start == 0 && t.end == 0 {
		return fmt.Sprintf("{%s%s %s %q %q}", t.pos, t.tok, t.sub, t.lit, t.unit)
	}

	return fmt.Sprintf("{%s%s %s %q %q 0x%x-0x%x}", t.pos, t.tok, t.sub, t.lit, t.unit, t.start, t.end)
}

type parseError struct {
	pos pos
	msg string
}

var scannerTests = []struct {
	name    string
	input   string
	want    []token
	wantErr []parseError
	pos     bool
}{
	{
		name:  "basic rule",
		input: `img  { foo: "Hello, 世界"  /* not a real rule */ }`,
		pos:   true,
		want: []token{
			{pos: pos{0, 0}, tok: Ident, lit: "img"},
			{pos: pos{0, 5}, tok: LeftBrace},
			{pos: pos{0, 7}, tok: Ident, lit: "foo"},
			{pos: pos{0, 10}, tok: Colon},
			{pos: pos{0, 12}, tok: String, lit: "Hello, 世界"},
			{pos: pos{0, 51}, tok: RightBrace}, // note: byte offset, UTF-8
			{pos: pos{0, 52}, tok: EOF},
		},
	},
	{
		input: `font-size: +2.34em; border: 0; fraction: .1; e: 1e-10;`,
		want: []token{
			{tok: Ident, lit: "font-size"},
			{tok: Colon},
			{tok: Dimension, sub: TypeFlagNumber, lit: "+2.34", unit: "em"},
			{tok: Semicolon},
			{tok: Ident, lit: "border"},
			{tok: Colon},
			{tok: Number, sub: TypeFlagInteger, lit: "0"},
			{tok: Semicolon},
			{tok: Ident, lit: "fraction"},
			{tok: Colon},
			{tok: Number, sub: TypeFlagNumber, lit: ".1"},
			{tok: Semicolon},
			{tok: Ident, lit: "e"},
			{tok: Colon},
			{tok: Number, sub: TypeFlagNumber, lit: "1e-10"},
			{tok: Semicolon},
			{tok: EOF},
		},
	},
	{
		input: `<!-- a || b |= c ~= @d *= e #f ua Ub -x \g -->`,
		want: []token{
			{tok: CDO},
			{tok: Ident, lit: "a"},
			{tok: Column},
			{tok: Ident, lit: "b"},
			{tok: DashMatch},
			{tok: Ident, lit: "c"},
			{tok: IncludeMatch},
			{tok: AtKeyword, lit: "d"},
			{tok: SubstringMatch},
			{tok: Ident, lit: "e"},
			{tok: Hash, lit: "f"},
			{tok: Ident, lit: "ua"},
			{tok: Ident, lit: "Ub"},
			{tok: Ident, lit: "-x"},
			{tok: Ident, lit: "g"},
			{tok: CDC},
			{tok: EOF},
		},
	},
	{
		name:  "unicode range tests",
		input: `u+0102?? u+01-05 u+Fa`,
		want: []token{
			{tok: UnicodeRange, start: 0x010200, end: 0x0102ff},
			{tok: UnicodeRange, start: 0x01, end: 0x05},
			{tok: UnicodeRange, start: 0xfa, end: 0xfa},
			{tok: EOF},
		},
	},
	{
		name:  "escape tests",
		input: `"a\d\a" 5`,
		want: []token{
			{tok: String, lit: "a\r\n"},
			{tok: Number, sub: TypeFlagInteger, lit: "5"},
			{tok: EOF},
		},
	},
	{
		name:  "infinite ident loop (from go-fuzz)",
		input: "\x80",
		want: []token{
			{tok: Ident, lit: "\uFFFD"},
			{tok: EOF},
		},
	},
	{
		name:  "infinite + loop (from go-fuzz)",
		input: "+",
		want: []token{
			{tok: Delim, lit: "+"},
			{tok: EOF},
		},
	},
	{
		name: "string newline",
		input: `"foo\
bar"`,
		want: []token{
			{tok: String, lit: "foo\nbar"},
			{tok: EOF},
		},
	},
	{
		name:  "bad string",
		input: `name: "foo`,
		want: []token{
			{tok: Ident, lit: "name"},
			{tok: Colon},
			{tok: BadString},
			{tok: EOF},
		},
		wantErr: []parseError{{pos{0, 10}, "unterminated string"}},
	},
	{
		name: "bad string newline",
		input: `name: "foo
`,
		want: []token{
			{tok: Ident, lit: "name"},
			{tok: Colon},
			{tok: BadString},
			{tok: EOF},
		},
		wantErr: []parseError{{pos{1, 0}, "newline in string"}},
	},
	{
		name:  "bad comment",
		input: `/* comment`,
		want: []token{
			{tok: EOF},
		},
		wantErr: []parseError{{pos{0, 10}, "unterminated comment"}},
	},

	{
		name:  "url tests",
		input: `background:url("https://example.com/foo"), url( data:foo\A  );`,
		want: []token{
			{tok: Ident, lit: "background"},
			{tok: Colon},
			{tok: URL, lit: "https://example.com/foo"},
			{tok: Comma},
			{tok: URL, lit: "data:foo\n"},
			{tok: Semicolon},
			{tok: EOF},
		},
	},
	{
		name:  "unterminated url",
		input: `bg: url('https://example.com`,
		want: []token{
			{tok: Ident, lit: "bg"},
			{tok: Colon},
			{tok: BadURL},
			{tok: EOF},
		},
		wantErr: []parseError{{pos{0, 28}, "unterminated string"}},
	},
	{
		name: "multiline",
		pos:  true,
		input: `a {
	text-decoration: none;
      border: 1px solid #1df;
}`,
		want: []token{
			{pos: pos{0, 0}, tok: Ident, lit: "a"},
			{pos: pos{0, 2}, tok: LeftBrace},
			{pos: pos{1, 1}, tok: Ident, lit: "text-decoration"},
			{pos: pos{1, 16}, tok: Colon},
			{pos: pos{1, 18}, tok: Ident, lit: "none"},
			{pos: pos{1, 22}, tok: Semicolon},
			{pos: pos{2, 6}, tok: Ident, lit: "border"}, // spaces, not tabs
			{pos: pos{2, 12}, tok: Colon},
			{pos: pos{2, 14}, tok: Dimension, sub: TypeFlagInteger, lit: "1", unit: "px"},
			{pos: pos{2, 18}, tok: Ident, lit: "solid"},
			{pos: pos{2, 24}, tok: Hash, lit: "1df"},
			{pos: pos{2, 28}, tok: Semicolon},
			{pos: pos{3, 0}, tok: RightBrace},
			{pos: pos{3, 1}, tok: EOF},
		},
	},
}

func TestScanner(t *testing.T) {
	for _, test := range scannerTests {
		name := test.name
		if name == "" {
			name = test.input
		}
		t.Run(name, func(t *testing.T) {
			var gotErr []parseError
			errh := func(line, col, n int, msg string) {
				if len(test.wantErr) > 0 {
					gotErr = append(gotErr, parseError{pos{line, col}, msg})
				} else {
					t.Errorf("%d:%d: (n=%d): %s", line, col, n, msg)
				}
			}
			s := NewScanner(strings.NewReader(test.input), errh)
			var got []token
			for {
				s.Next()
				tok := token{
					tok:   s.Token,
					lit:   string(s.Literal),
					sub:   s.TypeFlag,
					unit:  string(s.Unit),
					start: s.RangeStart,
					end:   s.RangeEnd,
				}
				if test.pos {
					tok.pos = pos{s.Line, s.Col}
				}
				got = append(got, tok)
				if s.Token == EOF {
					break
				}
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("got:\n\t%v\nwant:\n\t%v", got, test.want)
			}
			if !reflect.DeepEqual(gotErr, test.wantErr) {
				t.Errorf("got error:\n\t%v\nwant:\n\t%v", gotErr, test.wantErr)
			}
		})
	}
}

func TestScannerFiles(t *testing.T) {
	files, err := filepath.Glob("testdata/*.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			f, err := os.Open(file)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			errh := func(line, col, n int, msg string) {
				t.Errorf("%d:%d: (n=%d): %s", line, col, n, msg)
			}
			s := NewScanner(f, errh)
			for {
				s.Next()
				tok := token{
					pos:   pos{s.Line, s.Col},
					tok:   s.Token,
					lit:   string(s.Literal),
					sub:   s.TypeFlag,
					unit:  string(s.Unit),
					start: s.RangeStart,
					end:   s.RangeEnd,
				}
				t.Log(tok)

				if s.Token == EOF {
					break
				}
			}
		})
	}
}
