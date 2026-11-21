package documentruntime

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
)

// macOS LibreOffice registers a native spell checker,
// org.openoffice.lingu.MacOSXSpellChecker, whose constructor calls
// NSApplicationLoad() unconditionally (lingucomponent macspellimp.mm at the
// pinned build bce0998afefdbc355585ca324285661a2170ba77). Inside the
// seatbelt worker sandbox that AppKit registration reaches HIServices
// _RegisterApplication, is denied, and aborts the converter during LngSvcMgr
// service enumeration before any document renders. The build ships no
// setting or environment toggle for that constructor, so the supported
// bypass is the documented URE_MORE_SERVICES bootstrap override:
// sal/rtl/bootstrap.cxx resolves ambience values (rtl_bootstrap_set, -env:
// command line, environment) before ini files, so exporting
// URE_MORE_SERVICES with a filtered copy of the shipped registry removes
// exactly that one implementation while the signed app bundle stays
// untouched. HunSpell (org.openoffice.lingu.MySpellSpellChecker) keeps the
// SpellChecker service.
const converterRegistryNamespace = "http://openoffice.org/2010/uno-components"

const macSpellCheckerImplementation = "org.openoffice.lingu.MacOSXSpellChecker"

// registryNode records one element's source span during the single parse
// pass behind filterMacSpellChecker; the spans feed the byte-exact excision.
type registryNode struct {
	name         string
	start        int64
	impls        int
	target       bool
	targetParent bool
}

// filterMacSpellChecker strips the macOS-native spell-checker registration
// from a services.rdb payload. The registration is matched by parsed
// identity — element name plus implementation name attribute — so
// whitespace or attribute-order drift in the shipped file cannot make the
// filter silently pass. When the target was its component's only
// implementation the whole component is excised as well: the UNO registry
// parser's freshly-inside-component state accepts only an implementation
// begin item and rejects an empty component with InvalidRegistryException
// (pinned cppuhelper servicemanager state machine; proven in diagnostic
// .local/adr090/document-fullturn-xmlfix round 4). Bytes outside the
// excised span are preserved verbatim, so HunSpell keeps serving the
// SpellChecker service. A payload without the registration passes through
// unchanged; multiple registrations are refused rather than guessed.
func filterMacSpellChecker(data []byte) ([]byte, bool, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var stack []registryNode
	var hitStart, hitStop, compStart, compStop int64 = -1, -1, -1, -1
	compImpls := 0
	hits, impls, comps := 0, 0, 0
	rootSeen := false
	for {
		start := decoder.InputOffset()
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, false, fmt.Errorf("parse converter registry: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			if element.Name.Space != converterRegistryNamespace {
				return nil, false, fmt.Errorf("converter registry element %s has unexpected namespace %q", element.Name.Local, element.Name.Space)
			}
			node := registryNode{name: element.Name.Local, start: start}
			if len(stack) == 0 {
				if rootSeen {
					return nil, false, errors.New("converter registry has multiple root elements")
				}
				rootSeen = true
				if node.name != "components" {
					return nil, false, fmt.Errorf("converter registry root is %s, want components", node.name)
				}
			}
			if node.name == "implementation" {
				impls++
				if len(stack) > 0 && stack[len(stack)-1].name == "component" {
					stack[len(stack)-1].impls++
				}
				for _, attr := range element.Attr {
					if attr.Name.Space == "" && attr.Name.Local == "name" && attr.Value == macSpellCheckerImplementation {
						hits++
						node.target = true
						if len(stack) == 0 || stack[len(stack)-1].name != "component" {
							return nil, false, fmt.Errorf("%s registration is not a direct child of a component", macSpellCheckerImplementation)
						}
						stack[len(stack)-1].targetParent = true
					}
				}
			}
			if node.name == "component" {
				comps++
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, false, fmt.Errorf("converter registry has unbalanced %s end tag", element.Name.Local)
			}
			pos := len(stack) - 1
			stop := decoder.InputOffset()
			if stack[pos].target {
				hitStart, hitStop = stack[pos].start, stop
			}
			if stack[pos].targetParent {
				compStart, compStop, compImpls = stack[pos].start, stop, stack[pos].impls
			}
			stack = stack[:pos]
		}
	}
	if !rootSeen || len(stack) != 0 {
		return nil, false, errors.New("converter registry has unbalanced elements")
	}
	switch hits {
	case 0:
		return data, false, nil
	case 1:
	default:
		return nil, false, fmt.Errorf("converter registry carries %d %s registrations", hits, macSpellCheckerImplementation)
	}
	return exciseMacSpellChecker(data, hitStart, hitStop, compStart, compStop, compImpls, impls, comps)
}

// exciseMacSpellChecker removes and verifies only the selected registry span.
func exciseMacSpellChecker(data []byte, hitStart, hitStop, compStart, compStop int64, compImpls, impls, comps int) ([]byte, bool, error) {
	victimStart, victimStop := hitStart, hitStop
	compVictim := compImpls == 1
	if compVictim {
		victimStart, victimStop = compStart, compStop
	}
	if victimStart < 0 || victimStop <= victimStart || victimStart >= int64(len(data)) || data[victimStart] != '<' || data[victimStop-1] != '>' {
		return nil, false, errors.New("converter registry excision span is malformed")
	}
	a, b := int(victimStart), int(victimStop)
	out := append(data[:a:a], data[b:]...)
	wantComps := comps
	if compVictim {
		wantComps--
	}
	if err := verifyFilteredRegistry(out, impls-1, wantComps); err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// verifyFilteredRegistry re-parses the filtered payload and enforces the
// tripwires proven in diagnostic round 4: the implementation count drops by
// exactly one, the component count only when the whole component went, no
// component is left without a direct implementation child (the UNO registry
// parser rejects one), the spell-checker identity is gone, and the payload
// is well-formed XML.
func verifyFilteredRegistry(data []byte, wantImpls, wantComps int) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var stack []registryNode
	impls, comps := 0, 0
	rootSeen := false
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("parse filtered converter registry: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			if element.Name.Space != converterRegistryNamespace {
				return fmt.Errorf("converter registry element %s has unexpected namespace %q", element.Name.Local, element.Name.Space)
			}
			node := registryNode{name: element.Name.Local}
			if len(stack) == 0 {
				if rootSeen {
					return errors.New("filtered converter registry has multiple root elements")
				}
				rootSeen = true
				if node.name != "components" {
					return fmt.Errorf("filtered converter registry root is %s, want components", node.name)
				}
			}
			if node.name == "implementation" {
				impls++
				for _, attr := range element.Attr {
					if attr.Name.Space == "" && attr.Name.Local == "name" && attr.Value == macSpellCheckerImplementation {
						return fmt.Errorf("%s identity survived filtering", macSpellCheckerImplementation)
					}
				}
				if len(stack) > 0 && stack[len(stack)-1].name == "component" {
					stack[len(stack)-1].impls++
				}
			}
			if node.name == "component" {
				comps++
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) == 0 {
				return fmt.Errorf("filtered converter registry has unbalanced %s end tag", element.Name.Local)
			}
			pos := len(stack) - 1
			if stack[pos].name == "component" && stack[pos].impls == 0 {
				return errors.New("filtered converter registry leaves a component without an implementation")
			}
			stack = stack[:pos]
		}
	}
	if !rootSeen || len(stack) != 0 {
		return errors.New("filtered converter registry has unbalanced elements")
	}
	if impls != wantImpls {
		return fmt.Errorf("filtered converter registry implementations=%d want %d", impls, wantImpls)
	}
	if comps != wantComps {
		return fmt.Errorf("filtered converter registry components=%d want %d", comps, wantComps)
	}
	return nil
}

// validateManagedRegistry verifies the properties that make services.rdb safe
// to advertise to LibreOffice. It intentionally has no expected-count input:
// readiness cares that the published XML is structurally usable and that the
// native macOS implementation is absent, while filterMacSpellChecker performs
// the stronger before/after count comparison during construction.
func validateManagedRegistry(data []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var stack []registryNode
	rootSeen := false
	implementations := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("parse managed converter registry: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			if element.Name.Space != converterRegistryNamespace {
				return fmt.Errorf("converter registry element %s has unexpected namespace %q", element.Name.Local, element.Name.Space)
			}
			node := registryNode{name: element.Name.Local}
			if len(stack) == 0 {
				if rootSeen {
					return errors.New("managed converter registry has multiple root elements")
				}
				rootSeen = true
				if node.name != "components" {
					return fmt.Errorf("managed converter registry root is %s, want components", node.name)
				}
			}
			if node.name == "implementation" {
				implementations++
				if len(stack) > 0 && stack[len(stack)-1].name == "component" {
					stack[len(stack)-1].impls++
				}
				for _, attr := range element.Attr {
					if attr.Name.Space == "" && attr.Name.Local == "name" && attr.Value == macSpellCheckerImplementation {
						return fmt.Errorf("%s identity is present", macSpellCheckerImplementation)
					}
				}
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) == 0 {
				return fmt.Errorf("managed converter registry has unbalanced %s end tag", element.Name.Local)
			}
			pos := len(stack) - 1
			if stack[pos].name == "component" && stack[pos].impls == 0 {
				return errors.New("managed converter registry has a component without an implementation")
			}
			stack = stack[:pos]
		}
	}
	if len(stack) != 0 {
		return errors.New("managed converter registry has unbalanced elements")
	}
	if !rootSeen || implementations == 0 {
		return errors.New("managed converter registry has no implementations")
	}
	return nil
}

// FilterConverterRegistry produces the filtered services.rdb that ships in
// the Office preview component manifest (office-preview spec AD-3): the
// macOS spell-checker registration is excised and the result must still be a
// structurally usable registry without that identity. Packaging runs it once
// on the pinned bundle; no runtime registry store exists any more.
func FilterConverterRegistry(data []byte) ([]byte, error) {
	out, _, err := filterMacSpellChecker(data)
	if err != nil {
		return nil, err
	}
	if err := validateManagedRegistry(out); err != nil {
		return nil, err
	}
	return out, nil
}
