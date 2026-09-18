package documentruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
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

const (
	converterServiceDirName = "libreoffice-services"
	converterServiceRdbName = "services.rdb"
)

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

// ConverterServiceRegistryDir is the stable registry root. The active file
// selects an immutable generation; older generations remain available to
// converter processes that already received their URL.
func ConverterServiceRegistryDir(layout Layout) string {
	return filepath.Join(layout.Lib, converterServiceDirName)
}

// ConverterServiceOverride returns only a validated, immutable registry URL.
// A missing root means services were never configured. Once initialized,
// missing pointers or generations are errors, never a stock-registry fallback.
func ConverterServiceOverride(layout Layout) (string, error) {
	root := ConverterServiceRegistryDir(layout)
	info, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect converter registry root: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("converter registry root is not a directory")
	}
	active := filepath.Join(root, "active")
	info, err = os.Lstat(active)
	if err != nil {
		return "", fmt.Errorf("read active converter registry: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != sha256.Size*2 {
		return "", errors.New("invalid active converter registry pointer")
	}
	data, err := os.ReadFile(active)
	if err != nil {
		return "", fmt.Errorf("read active converter registry: %w", err)
	}
	name := string(data)
	decoded, err := hex.DecodeString(name)
	if err != nil || len(decoded) != sha256.Size || strings.ToLower(name) != name {
		return "", errors.New("invalid active converter registry pointer")
	}
	dir := filepath.Join(root, name)
	if err := verifyConverterGeneration(dir, name); err != nil {
		return "", err
	}
	return "<" + (&url.URL{Scheme: "file", Path: dir}).String() + ">*", nil
}

// PrepareConverterServiceRegistry validates and stores a new immutable
// generation without changing the active selection. Call the returned commit
// only after other runtime validation and manifest writes succeed. Abandoned
// generations are harmless and are retained for in-flight readers.
func PrepareConverterServiceRegistry(layout Layout, converter string) (func() error, error) {
	return prepareConverterServiceRegistry(layout, converter, runtime.GOOS)
}

// MaterializeConverterServiceRegistry prepares and commits a filtered registry.
func MaterializeConverterServiceRegistry(layout Layout, converter string) error {
	return materializeConverterServiceRegistry(layout, converter, runtime.GOOS)
}

func materializeConverterServiceRegistry(layout Layout, converter, goos string) error {
	commit, err := prepareConverterServiceRegistry(layout, converter, goos)
	if err != nil {
		return err
	}
	return commit()
}

func prepareConverterServiceRegistry(layout Layout, converter, goos string) (func() error, error) {
	if goos != "darwin" {
		return func() error { return nil }, nil
	}
	services, ok, err := converterBundleServicesDir(converter)
	if err != nil {
		return nil, err
	}
	if !ok {
		return func() error { return nil }, nil
	}
	entries, err := os.ReadDir(services)
	if err != nil {
		return nil, fmt.Errorf("read converter services registry: %w", err)
	}
	dst := ConverterServiceRegistryDir(layout)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, fmt.Errorf("create document lib directory: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(dst), "."+converterServiceDirName+".stage-")
	if err != nil {
		return nil, fmt.Errorf("stage converter services registry: %w", err)
	}
	defer os.RemoveAll(stage)
	sawRdb := false
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".rdb" {
			continue
		}
		if entry.IsDir() {
			return nil, fmt.Errorf("converter registry %s is not a regular file", entry.Name())
		}
		sawRdb = true
		data, err := os.ReadFile(filepath.Join(services, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read converter registry %s: %w", entry.Name(), err)
		}
		if entry.Name() == converterServiceRdbName {
			if data, _, err = filterMacSpellChecker(data); err != nil {
				return nil, err
			}
		}
		if err := fileutil.WriteFileAtomic(filepath.Join(stage, entry.Name()), data, 0o644); err != nil {
			return nil, fmt.Errorf("write converter registry %s: %w", entry.Name(), err)
		}
	}
	if !sawRdb {
		return nil, fmt.Errorf("converter services registry %s carries no .rdb files", services)
	}
	if err := os.Chmod(stage, 0o755); err != nil {
		return nil, fmt.Errorf("open staged converter services registry: %w", err)
	}
	usable, err := converterServiceRegistryUsable(stage)
	if err != nil {
		return nil, fmt.Errorf("staged converter services registry unusable: %w", err)
	}
	if !usable {
		return nil, errors.New("staged converter services registry is absent")
	}

	name, err := converterGenerationDigest(stage)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return nil, fmt.Errorf("create converter registry root: %w", err)
	}
	info, err := os.Lstat(dst)
	if err != nil || !info.IsDir() {
		return nil, errors.New("converter registry root is not a directory")
	}
	generation := filepath.Join(dst, name)
	if err := os.Rename(stage, generation); err != nil {
		// Another writer or previous preparation may already have stored the exact
		// same immutable content. Verify it rather than replacing its directory.
		if verifyErr := verifyConverterGeneration(generation, name); verifyErr != nil {
			return nil, fmt.Errorf("publish converter registry generation: %w (%v)", err, verifyErr)
		}
	}
	if err := verifyConverterGeneration(generation, name); err != nil {
		return nil, err
	}
	return func() error {
		if err := verifyConverterGeneration(generation, name); err != nil {
			return err
		}
		if err := fileutil.WriteFileAtomic(filepath.Join(dst, "active"), []byte(name), 0644); err != nil {
			return fmt.Errorf("publish active converter registry: %w", err)
		}
		return nil
	}, nil
}

func verifyConverterGeneration(dir, name string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect converter registry generation: %w", err)
	}
	if !info.IsDir() {
		return errors.New("converter registry generation is not a directory")
	}
	usable, err := converterServiceRegistryUsable(dir)
	if err != nil {
		return err
	}
	if !usable {
		return errors.New("converter registry generation is absent")
	}
	digest, err := converterGenerationDigest(dir)
	if err != nil {
		return err
	}
	if digest != name {
		return errors.New("converter registry generation checksum mismatch")
	}
	return nil
}

func converterGenerationDigest(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read converter registry generation: %w", err)
	}
	h := sha256.New()
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".rdb" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("converter registry %s is not a regular file", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%d:%s:%d:", len(entry.Name()), entry.Name(), len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// converterBundleServicesDir maps a converter executable inside a macOS app
// bundle (Contents/MacOS/soffice, following the bin/ symlink the installer
// publishes) to its Contents/Resources/services directory. A converter
// outside that shape is a supported flat layout and reports ok=false, so
// fixture and non-bundle installs keep stock behavior. A converter under
// Contents/MacOS declares a bundle, so an unreadable services registry is a
// broken install that errors instead of masquerading as a flat no-op.
func converterBundleServicesDir(converter string) (string, bool, error) {
	real, err := filepath.EvalSymlinks(converter)
	if err != nil {
		return "", false, fmt.Errorf("resolve converter executable: %w", err)
	}
	macOS := filepath.Dir(real)
	if filepath.Base(macOS) != "MacOS" || filepath.Base(filepath.Dir(macOS)) != "Contents" {
		return "", false, nil
	}
	services := filepath.Join(filepath.Dir(macOS), "Resources", "services")
	info, err := os.Stat(services)
	if err != nil {
		return "", false, fmt.Errorf("read converter app bundle services registry: %w", err)
	}
	if !info.IsDir() {
		return "", false, errors.New("converter app bundle services registry is not a directory")
	}
	return services, true, nil
}

// converterServiceRegistryUsable reports whether dir holds a complete
// managed registry: at least one .rdb file and a non-empty regular
// services.rdb. Absence (fs.ErrNotExist) is the one clean "not managed"
// answer; every other failure is returned so callers refuse to advertise or
// publish an incomplete registry.
func converterServiceRegistryUsable(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspect converter services registry: %w", err)
	}
	if !info.IsDir() {
		return false, errors.New("converter services registry is not a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("read converter services registry: %w", err)
	}
	hasRdb := false
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".rdb" {
			hasRdb = true
		}
	}
	if !hasRdb {
		return false, errors.New("converter services registry carries no .rdb files")
	}
	rdbPath := filepath.Join(dir, converterServiceRdbName)
	rdb, err := os.Stat(rdbPath)
	if err != nil {
		return false, fmt.Errorf("inspect converter %s: %w", converterServiceRdbName, err)
	}
	if !rdb.Mode().IsRegular() || rdb.Size() == 0 {
		return false, fmt.Errorf("converter %s is not a non-empty regular file", converterServiceRdbName)
	}
	data, err := os.ReadFile(rdbPath)
	if err != nil {
		return false, fmt.Errorf("read converter %s: %w", converterServiceRdbName, err)
	}
	if err := validateManagedRegistry(data); err != nil {
		return false, fmt.Errorf("validate converter %s: %w", converterServiceRdbName, err)
	}
	// The directory wildcard advertises every companion registry to UNO.
	// Validate each one before either publishing or returning the override.
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".rdb" || entry.Name() == converterServiceRdbName {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return false, fmt.Errorf("inspect converter %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return false, fmt.Errorf("converter %s is not a non-empty regular file", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return false, fmt.Errorf("read converter %s: %w", entry.Name(), err)
		}
		if err := validateManagedRegistry(data); err != nil {
			return false, fmt.Errorf("validate converter %s: %w", entry.Name(), err)
		}
	}
	return true, nil
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
