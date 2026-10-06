package mediaModel

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// SVG logos are rebuilt from an allowlist: only drawing elements and
// presentation attributes survive. Scripts, event handlers, foreign content,
// external references (href/url() to anything but a local #id), <style>
// blocks and DOCTYPE/entities are dropped, so the stored file cannot run
// code or load anything even when opened directly.

var svgElements = map[string]bool{
	"svg": true, "g": true, "path": true, "rect": true, "circle": true, "ellipse": true, "line": true, "polyline": true, "polygon": true,
	"text": true, "tspan": true, "defs": true, "lineargradient": true, "radialgradient": true, "stop": true, "clippath": true, "mask": true,
	"symbol": true, "use": true, "title": true, "desc": true,
}

var svgAttributes = map[string]bool{
	"id": true, "class": true, "d": true, "x": true, "y": true, "width": true, "height": true, "viewbox": true, "fill": true, "stroke": true,
	"transform": true, "points": true, "cx": true, "cy": true, "r": true, "rx": true, "ry": true, "x1": true, "y1": true, "x2": true, "y2": true,
	"fx": true, "fy": true, "offset": true, "stop-color": true, "stop-opacity": true, "opacity": true, "fill-opacity": true, "stroke-opacity": true,
	"stroke-width": true, "stroke-linecap": true, "stroke-linejoin": true, "stroke-miterlimit": true, "stroke-dasharray": true, "stroke-dashoffset": true,
	"fill-rule": true, "clip-rule": true, "clip-path": true, "mask": true, "gradientunits": true, "gradienttransform": true, "spreadmethod": true,
	"preserveaspectratio": true, "version": true, "font-family": true, "font-size": true, "font-weight": true, "font-style": true, "text-anchor": true,
	"dominant-baseline": true, "letter-spacing": true, "visibility": true, "display": true, "style": true, "href": true, "maskunits": true,
	"clippathunits": true, "patternunits": true,
}

// errSVGInvalid: not a well-formed SVG document.
var errSVGInvalid = errors.New("not an SVG document")

// safeSVGValue rejects values that reach outside the document.
func safeSVGValue(name, value string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(value), ""))
	if name == "href" {
		return strings.HasPrefix(value, "#")
	}
	// A backslash is a CSS escape: u\72l( is url( for the style engine but not for the text checks below.
	// Drawing values never need one.
	if strings.Contains(value, "\\") {
		return false
	}
	if strings.Contains(lower, "javascript:") || strings.Contains(lower, "expression(") || strings.Contains(lower, "@import") {
		return false
	}
	for rest := lower; ; {
		index := strings.Index(rest, "url(")
		if index < 0 {
			return true
		}
		target := strings.TrimLeft(rest[index+4:], `'"`)
		if !strings.HasPrefix(target, "#") {
			return false
		}
		rest = rest[index+4:]
	}
}

// SanitizeSVG returns the document rebuilt from the allowlist.
func SanitizeSVG(data []byte) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	var out bytes.Buffer
	encoder := xml.NewEncoder(&out)
	skipping, depth, root := 0, 0, false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errSVGInvalid
		}
		switch value := token.(type) {
		case xml.StartElement:
			name := strings.ToLower(value.Name.Local)
			if depth == 0 && name != "svg" {
				return nil, errSVGInvalid
			}
			depth++
			if skipping > 0 || !svgElements[name] {
				skipping++
				continue
			}
			root = true
			clean := xml.StartElement{Name: xml.Name{Local: value.Name.Local}}
			for _, attr := range value.Attr {
				local := strings.ToLower(attr.Name.Local)
				if attr.Name.Space == "xmlns" || local == "xmlns" {
					continue
				}
				if !svgAttributes[local] || strings.HasPrefix(local, "on") || !safeSVGValue(local, attr.Value) {
					continue
				}
				clean.Attr = append(clean.Attr, xml.Attr{Name: xml.Name{Local: attr.Name.Local}, Value: attr.Value})
			}
			if name == "svg" && depth == 1 {
				clean.Attr = append(clean.Attr, xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: "http://www.w3.org/2000/svg"})
			}
			if err = encoder.EncodeToken(clean); err != nil {
				return nil, err
			}
		case xml.EndElement:
			depth--
			if skipping > 0 {
				skipping--
				continue
			}
			if err = encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: value.Name.Local}}); err != nil {
				return nil, err
			}
		case xml.CharData:
			if skipping == 0 && depth > 0 {
				if err = encoder.EncodeToken(value.Copy()); err != nil {
					return nil, err
				}
			}
		}
		// Comments, processing instructions and directives (DOCTYPE,
		// entities) are dropped.
	}
	if !root {
		return nil, errSVGInvalid
	}
	if err := encoder.Flush(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// LooksLikeSVG is a cheap sniff before sanitizing: an XML or <svg> start.
func LooksLikeSVG(data []byte) bool {
	head := strings.ToLower(strings.TrimSpace(string(data[:min(len(data), 1024)])))
	head = strings.TrimPrefix(head, "\ufeff")
	return strings.HasPrefix(head, "<?xml") || strings.HasPrefix(head, "<svg") || strings.HasPrefix(head, "<!--")
}
