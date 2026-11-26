package server

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/apimgr/countries/src/common/i18n"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	xhtml "golang.org/x/net/html"
)

const maxOperatorPageBody = 256 * 1024

func newPageHTMLPolicy(rich bool) *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	elements := []string{"p", "br", "span", "div", "strong", "b", "em", "i", "u", "s", "small", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "a", "img"}
	if rich {
		elements = append(elements, "hr", "code", "pre", "blockquote", "table", "thead", "tbody", "tr", "th", "td", "dl", "dt", "dd", "sup", "sub")
	}
	policy.AllowElements(elements...)
	policy.AllowAttrs("href", "title", "target", "rel").OnElements("a")
	policy.AllowAttrs("src", "alt", "title", "width", "height").OnElements("img")
	policy.AllowAttrs("class", "id").Globally()
	policy.AllowRelativeURLs(true)
	policy.AllowURLSchemes("http", "https", "mailto", "tel", "data")
	policy.RequireNoReferrerOnLinks(true)
	return policy
}

var strictHTMLPolicy = newPageHTMLPolicy(false)
var richHTMLPolicy = newPageHTMLPolicy(true)

func markdownToHTML(source string) template.HTML {
	return template.HTML(sanitizePageHTML(markdownSourceToHTML(source), richHTMLPolicy))
}

func textToHTML(source string) template.HTML {
	paragraphs := strings.Split(strings.TrimSpace(source), "\n\n")
	var rendered strings.Builder
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		rendered.WriteString("<p>")
		rendered.WriteString(template.HTMLEscapeString(strings.ReplaceAll(paragraph, "\n", " ")))
		rendered.WriteString("</p>")
	}
	return template.HTML(rendered.String())
}

func (s *Server) operatorHTML(r *http.Request, name string) template.HTML {
	entry, ok := s.config.Server.Branding.Content[name]
	if !ok {
		return ""
	}
	body, ok := localizedPageBody(entry.Body, requestLanguage(r))
	if !ok || len(body) > maxOperatorPageBody {
		return ""
	}
	body = substitutePageVariables(body, s.version, s.buildDate)
	format := strings.ToLower(strings.TrimSpace(entry.Format))
	if format == "" {
		format = "md"
	}
	switch format {
	case "md":
		if name == "header" || name == "footer" {
			return ""
		}
		return template.HTML(sanitizePageHTML(markdownSourceToHTML(body), richHTMLPolicy))
	case "html":
		policy := richHTMLPolicy
		if name == "header" || name == "footer" {
			policy = strictHTMLPolicy
		}
		return template.HTML(sanitizePageHTML(body, policy))
	case "txt":
		if name == "header" || name == "footer" {
			return ""
		}
		return textToHTML(body)
	default:
		return ""
	}
}

func markdownSourceToHTML(source string) string {
	var rendered bytes.Buffer
	if err := goldmark.Convert([]byte(source), &rendered); err != nil {
		return "<p>Content unavailable.</p>"
	}
	return strings.ReplaceAll(strings.ReplaceAll(rendered.String(), "<h1>", "<h2>"), "</h1>", "</h2>")
}

func sanitizePageHTML(source string, policy *bluemonday.Policy) string {
	sanitized := policy.Sanitize(source)
	root, err := xhtml.Parse(strings.NewReader(sanitized))
	if err != nil {
		return ""
	}
	sanitizeDataURLs(root)
	var removeContactLinks func(*xhtml.Node)
	removeContactLinks = func(node *xhtml.Node) {
		for child := node.FirstChild; child != nil; {
			next := child.NextSibling
			if child.Type == xhtml.ElementNode && child.Data == "a" && contactFormHref(child) {
				for grandchild := child.FirstChild; grandchild != nil; {
					grandchildNext := grandchild.NextSibling
					child.RemoveChild(grandchild)
					node.InsertBefore(grandchild, child)
					grandchild = grandchildNext
				}
				node.RemoveChild(child)
			} else {
				removeContactLinks(child)
			}
			child = next
		}
	}
	removeContactLinks(root)
	fragment := root
	var findBody func(*xhtml.Node)
	findBody = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && node.Data == "body" {
			fragment = node
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			findBody(child)
			if fragment != root {
				return
			}
		}
	}
	findBody(root)
	var output bytes.Buffer
	for child := fragment.FirstChild; child != nil; child = child.NextSibling {
		if err := xhtml.Render(&output, child); err != nil {
			return ""
		}
	}
	return output.String()
}

func sanitizeDataURLs(root *xhtml.Node) {
	if root.Type == xhtml.ElementNode {
		for index := 0; index < len(root.Attr); {
			attribute := root.Attr[index]
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(attribute.Val)), "data:") &&
				(root.Data != "img" || attribute.Key != "src" || !isSafeRasterDataURL(attribute.Val)) {
				root.Attr = append(root.Attr[:index], root.Attr[index+1:]...)
				continue
			}
			index++
		}
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		sanitizeDataURLs(child)
	}
}

func isSafeRasterDataURL(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < len("data:") || !strings.EqualFold(value[:len("data:")], "data:") {
		return false
	}
	metadata, _, found := strings.Cut(value[len("data:"):], ",")
	if !found {
		return false
	}
	mediaType, _, _ := strings.Cut(metadata, ";")
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/avif", "image/gif", "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

func contactFormHref(anchor *xhtml.Node) bool {
	for _, attribute := range anchor.Attr {
		if attribute.Key != "href" {
			continue
		}
		value := strings.ToLower(strings.TrimSpace(attribute.Val))
		return value == "/server/contact" || strings.HasPrefix(value, "/server/contact?") || strings.HasPrefix(value, "/server/contact#")
	}
	return false
}

func localizedPageBody(body any, language string) (string, bool) {
	if value, ok := body.(string); ok {
		return value, true
	}
	values := make(map[string]string)
	switch body := body.(type) {
	case map[string]any:
		for key, value := range body {
			if text, ok := value.(string); ok {
				values[key] = text
			}
		}
	case map[string]string:
		for key, value := range body {
			values[key] = value
		}
	default:
		return "", false
	}
	if value, ok := values[language]; ok {
		return value, true
	}
	if value, ok := values["default"]; ok {
		return value, true
	}
	return "", false
}

func requestLanguage(r *http.Request) string {
	if language := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("lang"))); i18n.IsSupported(language) {
		return language
	}
	if cookie, err := r.Cookie("lang"); err == nil && i18n.IsSupported(cookie.Value) {
		return strings.ToLower(cookie.Value)
	}
	for _, language := range acceptedLanguages(r.Header.Get("Accept-Language")) {
		if separator := strings.IndexByte(language, '-'); separator >= 0 {
			language = language[:separator]
		}
		if i18n.IsSupported(language) {
			return language
		}
	}
	return "en"
}

func substitutePageVariables(body, version, buildDate string) string {
	buildTime := buildDate
	if parsed, err := time.Parse(time.RFC3339, buildDate); err == nil {
		buildTime = parsed.Format("January 02, 2006 at 15:04:05 MST")
	}
	values := map[string]string{
		"{current_year}":    fmt.Sprint(time.Now().Year()),
		"{project_name}":    "countries",
		"{project_org}":     "apimgr",
		"{project_version}": version,
		"{build_datetime}":  buildTime,
		"{onion_address}":   "",
		"{i2p_address}":     "",
		"{app_name}":        "Countries API",
	}
	for placeholder, value := range values {
		body = strings.ReplaceAll(body, placeholder, value)
	}
	return body
}
