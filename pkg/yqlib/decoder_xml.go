package yqlib

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"golang.org/x/net/html/charset"
	yaml "gopkg.in/yaml.v3"
)

type xmlDecoder struct {
	reader       io.Reader
	readAnything bool
	finished     bool
	prefs        XmlPreferences
}

func NewXMLDecoder(prefs XmlPreferences) Decoder {
	return &xmlDecoder{
		finished: false,
		prefs:    prefs,
	}
}

func (dec *xmlDecoder) Init(reader io.Reader) error {
	dec.reader = reader
	dec.readAnything = false
	dec.finished = false
	return nil
}

func (dec *xmlDecoder) createSequence(nodes []*xmlNode) (*yaml.Node, error) {
	yamlNode := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, child := range nodes {
		yamlChild, err := dec.convertToYamlNode(child)
		if err != nil {
			return nil, err
		}
		yamlNode.Content = append(yamlNode.Content, yamlChild)
	}

	return yamlNode, nil
}

func (dec *xmlDecoder) processComment(c string) string {
	if c == "" {
		return ""
	}
	return "#" + strings.TrimRight(c, " ")
}

func (dec *xmlDecoder) createMap(n *xmlNode) (*yaml.Node, error) {
	log.Debug("createMap: headC: %v, lineC: %v, footC: %v", n.HeadComment, n.LineComment, n.FootComment)
	yamlNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	if len(n.Data) > 0 {
		log.Debug("creating content node for map")
		label := dec.prefs.ContentName
		labelNode := createScalarNode(label, label)
		labelNode.HeadComment = dec.processComment(n.HeadComment)
		labelNode.LineComment = dec.processComment(n.LineComment)
		labelNode.FootComment = dec.processComment(n.FootComment)
		yamlNode.Content = append(yamlNode.Content, labelNode, dec.createValueNodeFromData(n.Data))
	}

	for i, keyValuePair := range n.Children {
		label := keyValuePair.K
		children := keyValuePair.V
		labelNode := createScalarNode(label, label)
		var valueNode *yaml.Node
		var err error

		if i == 0 {
			labelNode.HeadComment = dec.processComment(n.HeadComment)

		}

		labelNode.FootComment = dec.processComment(keyValuePair.FootComment)

		log.Debug("len of children in %v is %v", label, len(children))
		if len(children) > 1 {
			valueNode, err = dec.createSequence(children)
			if err != nil {
				return nil, err
			}
		} else {
			log.Debug("before hack, this is the data len: %", len(children[0].Data))
			// comment hack for maps of scalars
			// if the value is a scalar, the head comment of the scalar needs to go on the key?
			// add tests for <z/> as well as multiple <ds> of inputXmlWithComments > yaml
			if len(children[0].Children) == 0 && children[0].HeadComment != "" {
				if len(children[0].Data) > 0 {

					log.Debug("scalar comment hack")
					labelNode.HeadComment = labelNode.HeadComment + "\n" + strings.TrimSpace(children[0].HeadComment)
					children[0].HeadComment = ""
				} else {
					// child is null, put the headComment as a linecomment for reasons
					children[0].LineComment = children[0].HeadComment
					children[0].HeadComment = ""
				}
			}
			valueNode, err = dec.convertToYamlNode(children[0])
			if err != nil {
				return nil, err
			}
		}
		yamlNode.Content = append(yamlNode.Content, labelNode, valueNode)
	}

	return yamlNode, nil
}

func (dec *xmlDecoder) createValueNodeFromData(values []string) *yaml.Node {
	switch len(values) {
	case 0:
		return createScalarNode(nil, "")
	case 1:
		return createScalarNode(values[0], values[0])
	default:
		content := make([]*yaml.Node, 0)
		for _, value := range values {
			content = append(content, createScalarNode(value, value))
		}
		return &yaml.Node{
			Kind:    yaml.SequenceNode,
			Tag:     "!!seq",
			Content: content,
		}
	}
}

func (dec *xmlDecoder) convertToYamlNode(n *xmlNode) (*yaml.Node, error) {
	if len(n.Children) > 0 {
		return dec.createMap(n)
	}

	scalar := dec.createValueNodeFromData(n.Data)

	log.Debug("scalar (%v), headC: %v, lineC: %v, footC: %v", scalar.Tag, n.HeadComment, n.LineComment, n.FootComment)
	scalar.HeadComment = dec.processComment(n.HeadComment)
	scalar.LineComment = dec.processComment(n.LineComment)
	if scalar.Tag == "!!seq" {
		scalar.Content[0].HeadComment = scalar.LineComment
		scalar.LineComment = ""
	}

	scalar.FootComment = dec.processComment(n.FootComment)

	return scalar, nil
}

func (dec *xmlDecoder) Decode() (*CandidateNode, error) {
	if dec.finished {
		return nil, io.EOF
	}
	root := &xmlNode{}
	// cant use xj - it doesn't keep map order.
	err := dec.decodeXML(root)

	if err != nil {
		return nil, err
	}
	firstNode, err := dec.convertToYamlNode(root)

	if err != nil {
		return nil, err
	} else if firstNode.Tag == "!!null" {
		dec.finished = true
		if dec.readAnything {
			return nil, io.EOF
		}
	}
	dec.readAnything = true
	dec.finished = true

	return &CandidateNode{
		Node: &yaml.Node{
			Kind:    yaml.DocumentNode,
			Content: []*yaml.Node{firstNode},
		},
	}, nil
}

type xmlNode struct {
	Children    []*xmlChildrenKv
	HeadComment string
	FootComment string
	LineComment string
	Data        []string
}

type xmlChildrenKv struct {
	K           string
	V           []*xmlNode
	FootComment string
}

// AddChild appends a node to the list of children
func (n *xmlNode) AddChild(s string, c *xmlNode) {

	if n.Children == nil {
		n.Children = make([]*xmlChildrenKv, 0)
	}
	log.Debug("looking for %s", s)
	// see if we can find an existing entry to add to
	for _, childEntry := range n.Children {
		if childEntry.K == s {
			log.Debug("found it, appending an entry%s", s)
			childEntry.V = append(childEntry.V, c)
			log.Debug("yay len of children in %v is %v", s, len(childEntry.V))
			return
		}
	}
	log.Debug("not there, making a new one %s", s)
	n.Children = append(n.Children, &xmlChildrenKv{K: s, V: []*xmlNode{c}})
}

type element struct {
	parent *element
	n      *xmlNode
	label  string
	state  string
}

// this code is heavily based on https://github.com/basgys/goxml2json
// main changes are to decode into a structure that preserves the original order
// of the map keys.
func (dec *xmlDecoder) decodeXML(root *xmlNode) error {
	xmlDec := xml.NewDecoder(dec.reader)
	xmlDec.Strict = dec.prefs.StrictMode
	// That will convert the charset if the provided XML is non-UTF-8
	xmlDec.CharsetReader = charset.NewReaderLabel

	started := false

	// Create first element from the root node
	elem := &element{
		parent: nil,
		n:      root,
	}

	getToken := func() (xml.Token, error) {
		if dec.prefs.UseRawToken {
			return xmlDec.RawToken()
		}
		return xmlDec.Token()
	}

	for {
		t, e := getToken()
		if e != nil && !errors.Is(e, io.EOF) {
			return e
		}
		if t == nil {
			break
		}

		switch se := t.(type) {
		case xml.StartElement:
			log.Debug("start element %v", se.Name.Local)
			elem.state = "started"
			// Build new a new current element and link it to its parent
			elem = &element{
				parent: elem,
				n:      &xmlNode{},
				label:  se.Name.Local,
			}

			// Extract attributes as children
			for _, a := range se.Attr {
				if dec.prefs.KeepNamespace {
					if a.Name.Space != "" {
						a.Name.Local = a.Name.Space + ":" + a.Name.Local
					}
				}
				elem.n.AddChild(dec.prefs.AttributePrefix+a.Name.Local, &xmlNode{Data: []string{a.Value}})
			}
		case xml.CharData:

			// Extract XML data (if any)
			newBit := trimNonGraphic(string(se))
			if !started && len(newBit) > 0 {
				return fmt.Errorf("invalid XML: Encountered chardata [%v] outside of XML node", newBit)
			}

			if len(newBit) > 0 {
				elem.n.Data = append(elem.n.Data, newBit)
				elem.state = "chardata"
				log.Debug("chardata [%v] for %v", elem.n.Data, elem.label)
			}
		case xml.EndElement:
			log.Debug("end element %v", elem.label)
			elem.state = "finished"
			// And add it to its parent list
			if elem.parent != nil {
				elem.parent.n.AddChild(elem.label, elem.n)
			}

			// Then change the current element to its parent
			elem = elem.parent
		case xml.Comment:

			commentStr := string(xml.CharData(se))
			if elem.state == "started" {
				applyFootComment(elem, commentStr)

			} else if elem.state == "chardata" {
				log.Debug("got a line comment for (%v) %v: [%v]", elem.state, elem.label, commentStr)
				elem.n.LineComment = joinFilter([]string{elem.n.LineComment, commentStr})
			} else {
				log.Debug("got a head comment for (%v) %v: [%v]", elem.state, elem.label, commentStr)
				elem.n.HeadComment = joinFilter([]string{elem.n.HeadComment, commentStr})
			}

		case xml.ProcInst:
			if !dec.prefs.SkipProcInst {
				elem.n.AddChild(dec.prefs.ProcInstPrefix+se.Target, &xmlNode{Data: []string{string(se.Inst)}})
			}
		case xml.Directive:
			if !dec.prefs.SkipDirectives {
				elem.n.AddChild(dec.prefs.DirectiveName, &xmlNode{Data: []string{string(se)}})
			}
		}
		started = true
	}

	return nil
}

func applyFootComment(elem *element, commentStr string) {

	// first lets try to put the comment on the last child
	if len(elem.n.Children) > 0 {
		lastChildIndex := len(elem.n.Children) - 1
		childKv := elem.n.Children[lastChildIndex]
		log.Debug("got a foot comment for %v: [%v]", childKv.K, commentStr)
		childKv.FootComment = joinFilter([]string{elem.n.FootComment, commentStr})
	} else {
		log.Debug("got a foot comment for %v: [%v]", elem.label, commentStr)
		elem.n.FootComment = joinFilter([]string{elem.n.FootComment, commentStr})
	}
}

func joinFilter(rawStrings []string) string {
	stringsToJoin := make([]string, 0)
	for _, str := range rawStrings {
		if str != "" {
			stringsToJoin = append(stringsToJoin, str)
		}
	}
	return strings.Join(stringsToJoin, " ")
}

// trimNonGraphic returns a slice of the string s, with all leading and trailing
// non graphic characters and spaces removed.
//
// Graphic characters include letters, marks, numbers, punctuation, symbols,
// and spaces, from categories L, M, N, P, S, Zs.
// Spacing characters are set by category Z and property Pattern_White_Space.
func trimNonGraphic(s string) string {
	if s == "" {
		return s
	}

	var first *int
	var last int
	for i, r := range []rune(s) {
		if !unicode.IsGraphic(r) || unicode.IsSpace(r) {
			continue
		}

		if first == nil {
			f := i // copy i
			first = &f
			last = i
		} else {
			last = i
		}
	}

	// If first is nil, it means there are no graphic characters
	if first == nil {
		return ""
	}

	return string([]rune(s)[*first : last+1])
}
