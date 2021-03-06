// Copyright 2015 The Hugo Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package parser

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
)

const (
	HTML_LEAD          = "<"
	YAML_LEAD          = "-"
	YAML_DELIM_UNIX    = "---\n"
	YAML_DELIM_DOS     = "---\r\n"
	YAML_DELIM         = "---"
	TOML_LEAD          = "+"
	TOML_DELIM_UNIX    = "+++\n"
	TOML_DELIM_DOS     = "+++\r\n"
	TOML_DELIM         = "+++"
	JSON_LEAD          = "{"
	HTML_COMMENT_START = "<!--"
	HTML_COMMENT_END   = "-->"
)

var (
	delims = regexp.MustCompile(
		"^(" + regexp.QuoteMeta(YAML_DELIM) + `\s*\n|` + regexp.QuoteMeta(TOML_DELIM) + `\s*\n|` + regexp.QuoteMeta(JSON_LEAD) + ")",
	)

	UnixEnding = []byte("\n")
	DosEnding  = []byte("\r\n")
)

type FrontMatter []byte
type Content []byte

type Page interface {
	FrontMatter() FrontMatter
	Content() Content
	IsRenderable() bool
	Metadata() (interface{}, error)
}

type page struct {
	render      bool
	frontmatter FrontMatter
	content     Content
}

func (p *page) Content() Content {
	return p.content
}

func (p *page) FrontMatter() FrontMatter {
	return p.frontmatter
}

func (p *page) IsRenderable() bool {
	return p.render
}

func (p *page) Metadata() (meta interface{}, err error) {
	frontmatter := p.FrontMatter()

	if len(frontmatter) != 0 {
		fm := detectFrontMatter(rune(frontmatter[0]))
		meta, err = fm.Parse(frontmatter)
		if err != nil {
			return
		}
	}
	return
}

// ReadFrom reads the content from an io.Reader and constructs a page.
func ReadFrom(r io.Reader) (p Page, err error) {
	reader := bufio.NewReader(r)

	if err = chompWhitespace(reader); err != nil && err != io.EOF {
		return
	}
	if err = chompFrontmatterStartComment(reader); err != nil && err != io.EOF {
		return
	}

	firstLine, err := peekLine(reader)
	if err != nil && err != io.EOF {
		return
	}

	newp := new(page)
	newp.render = shouldRender(firstLine)

	if newp.render && isFrontMatterDelim(firstLine) {
		left, right := determineDelims(firstLine)
		fm, err := extractFrontMatterDelims(reader, left, right)
		if err != nil {
			return nil, err
		}
		newp.frontmatter = fm
	}

	content, err := extractContent(reader)
	if err != nil {
		return nil, err
	}

	newp.content = content

	return newp, nil
}

func chompWhitespace(r io.RuneScanner) (err error) {
	for {
		c, _, err := r.ReadRune()
		if err != nil {
			return err
		}
		if !unicode.IsSpace(c) {
			r.UnreadRune()
			return nil
		}
	}
}

func chompFrontmatterStartComment(r *bufio.Reader) (err error) {
	candidate, err := r.Peek(32)
	if err != nil {
		return err
	}

	str := string(candidate)
	if strings.HasPrefix(str, HTML_COMMENT_START) {
		lineEnd := strings.IndexAny(str, "\n")
		if lineEnd == -1 {
			//TODO: if we can't find it, Peek more?
			return nil
		}
		testStr := strings.TrimSuffix(str[0:lineEnd], "\r")
		if strings.Index(testStr, HTML_COMMENT_END) != -1 {
			return nil
		}
		buf := make([]byte, lineEnd)
		if _, err = r.Read(buf); err != nil {
			return
		}
		if err = chompWhitespace(r); err != nil {
			return err
		}
	}

	return nil
}

func chompFrontmatterEndComment(r *bufio.Reader) (err error) {
	candidate, err := r.Peek(32)
	if err != nil {
		return err
	}

	str := string(candidate)
	lineEnd := strings.IndexAny(str, "\n")
	if lineEnd == -1 {
		return nil
	}
	testStr := strings.TrimSuffix(str[0:lineEnd], "\r")
	if strings.Index(testStr, HTML_COMMENT_START) != -1 {
		return nil
	}

	//TODO: if we can't find it, Peek more?
	if strings.HasSuffix(testStr, HTML_COMMENT_END) {
		buf := make([]byte, lineEnd)
		if _, err = r.Read(buf); err != nil {
			return
		}
		if err = chompWhitespace(r); err != nil {
			return err
		}
	}

	return nil
}

func peekLine(r *bufio.Reader) (line []byte, err error) {
	firstFive, err := r.Peek(5)
	if err != nil {
		return
	}
	idx := bytes.IndexByte(firstFive, '\n')
	if idx == -1 {
		return firstFive, nil
	}
	idx++ // include newline.
	return firstFive[:idx], nil
}

func shouldRender(lead []byte) (frontmatter bool) {
	if len(lead) <= 0 {
		return
	}

	if bytes.Equal(lead[:1], []byte(HTML_LEAD)) {
		return
	}
	return true
}

func isFrontMatterDelim(data []byte) bool {
	return delims.Match(data)
}

func determineDelims(firstLine []byte) (left, right []byte) {
	switch len(firstLine) {
	case 5:
		fallthrough
	case 4:
		if firstLine[0] == YAML_LEAD[0] {
			return []byte(YAML_DELIM), []byte(YAML_DELIM)
		}
		return []byte(TOML_DELIM), []byte(TOML_DELIM)
	case 3:
		fallthrough
	case 2:
		fallthrough
	case 1:
		return []byte(JSON_LEAD), []byte("}")
	default:
		panic(fmt.Sprintf("Unable to determine delims from %q", firstLine))
	}
}

// extractFrontMatterDelims takes a frontmatter from the content bufio.Reader.
// Beginning white spaces of the bufio.Reader must be trimmed before call this
// function.
func extractFrontMatterDelims(r *bufio.Reader, left, right []byte) (fm FrontMatter, err error) {
	var (
		c         byte
		buf       bytes.Buffer
		level     int
		sameDelim = bytes.Equal(left, right)
	)

	// Frontmatter must start with a delimiter. To check it first,
	// pre-reads beginning delimiter length - 1 bytes from Reader
	for i := 0; i < len(left)-1; i++ {
		if c, err = r.ReadByte(); err != nil {
			return nil, fmt.Errorf("unable to read frontmatter at filepos %d: %s", buf.Len(), err)
		}
		if err = buf.WriteByte(c); err != nil {
			return nil, err
		}
	}

	// Reads a character from Reader one by one and checks it matches the
	// last character of one of delimiters to find the last character of
	// frontmatter. If it matches, makes sure it contains the delimiter
	// and if so, also checks it is followed by CR+LF or LF when YAML,
	// TOML case. In JSON case, nested delimiters must be parsed and it
	// is expected that the delimiter only contains one character.
	for {
		if c, err = r.ReadByte(); err != nil {
			return nil, fmt.Errorf("unable to read frontmatter at filepos %d: %s", buf.Len(), err)
		}
		if err = buf.WriteByte(c); err != nil {
			return nil, err
		}

		switch c {
		case left[len(left)-1]:
			if sameDelim { // YAML, TOML case
				if bytes.HasSuffix(buf.Bytes(), left) && (buf.Len() == len(left) || buf.Bytes()[buf.Len()-len(left)-1] == '\n') {
				nextByte:
					c, err = r.ReadByte()
					if err != nil {
						// It is ok that the end delimiter ends with EOF
						if err != io.EOF || level != 1 {
							return nil, fmt.Errorf("unable to read frontmatter at filepos %d: %s", buf.Len(), err)
						}
					} else {
						switch c {
						case '\n':
							// ok
						case ' ':
							// Consume this byte and try to match again
							goto nextByte
						case '\r':
							if err = buf.WriteByte(c); err != nil {
								return nil, err
							}
							if c, err = r.ReadByte(); err != nil {
								return nil, fmt.Errorf("unable to read frontmatter at filepos %d: %s", buf.Len(), err)
							}
							if c != '\n' {
								return nil, fmt.Errorf("frontmatter delimiter must be followed by CR+LF or LF but those can't be found at filepos %d", buf.Len())
							}
						default:
							return nil, fmt.Errorf("frontmatter delimiter must be followed by CR+LF or LF but those can't be found at filepos %d", buf.Len())
						}
						if err = buf.WriteByte(c); err != nil {
							return nil, err
						}
					}
					if level == 0 {
						level = 1
					} else {
						level = 0
					}
				}
			} else { // JSON case
				level++
			}
		case right[len(right)-1]: // JSON case only reaches here
			level--
		}

		if level == 0 {
			// Consumes white spaces immediately behind frontmatter
			if err = chompWhitespace(r); err != nil && err != io.EOF {
				return nil, err
			}
			if err = chompFrontmatterEndComment(r); err != nil && err != io.EOF {
				return nil, err
			}

			return buf.Bytes(), nil
		}
	}
}

func extractContent(r io.Reader) (content Content, err error) {
	wr := new(bytes.Buffer)
	if _, err = wr.ReadFrom(r); err != nil {
		return
	}
	return wr.Bytes(), nil
}
