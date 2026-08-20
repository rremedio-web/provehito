// Command sourceguard checks engine Go sources for operations that would cross
// Provehito's Phase 1 authority boundary. Configured subprocesses remain
// outside that boundary only in explicitly allowlisted engine files.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Finding describes one source-level authority violation.
type Finding struct {
	Path string
	Line int
	Rule string
}

var dynamicExecAllowlist = map[string]struct{}{
	filepath.ToSlash(filepath.Join("core", "process", "supervisor.go")):      {},
	filepath.ToSlash(filepath.Join("core", "fingerprint", "runner.go")):      {},
	filepath.ToSlash(filepath.Join("cmd", "provehito", "commands_state.go")): {},
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	findings, err := checkDir(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "source guard error: %v\n", err)
		os.Exit(1)
	}
	if len(findings) == 0 {
		fmt.Println("source guard: PASS")
		return
	}
	for _, finding := range findings {
		fmt.Printf("%s:%d: %s\n", finding.Path, finding.Line, finding.Rule)
	}
	os.Exit(1)
}

func checkDir(root string) ([]Finding, error) {
	var findings []Finding
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "testdata":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		fileFindings, err := checkFile(path)
		if err != nil {
			return err
		}
		findings = append(findings, fileFindings...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}

func checkFile(path string) ([]Finding, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	imports := importedPackages(file)
	values := collectValues(file)
	var findings []Finding

	httpImportFound := false
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err == nil && importPath == "net/http" {
			line := fileSet.Position(spec.Pos()).Line
			findings = append(findings, Finding{Path: path, Line: line, Rule: "http client"})
			httpImportFound = true
			break
		}
	}

	ast.Inspect(file, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.CallExpr:
			findings = append(findings, checkCall(path, fileSet, current, imports, values, httpImportFound)...)
		}
		return true
	})
	return findings, nil
}

type importedPackage struct {
	path  string
	local string
}

func importedPackages(file *ast.File) map[string]importedPackage {
	result := make(map[string]importedPackage)
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		local := filepath.Base(path)
		if spec.Name != nil {
			local = spec.Name.Name
		}
		if local == "_" || local == "." {
			continue
		}
		result[local] = importedPackage{path: path, local: local}
	}
	return result
}

type sourceValues struct {
	strings map[string]string
	slices  map[string][]string
}

func collectValues(file *ast.File) sourceValues {
	values := sourceValues{strings: make(map[string]string), slices: make(map[string][]string)}
	for pass := 0; pass < 3; pass++ {
		ast.Inspect(file, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.ValueSpec:
				for index, name := range current.Names {
					if index >= len(current.Values) {
						continue
					}
					values.record(name.Name, current.Values[index])
				}
			case *ast.AssignStmt:
				for index, lhs := range current.Lhs {
					name, ok := lhs.(*ast.Ident)
					if !ok || index >= len(current.Rhs) {
						continue
					}
					values.record(name.Name, current.Rhs[index])
				}
			}
			return true
		})
	}
	return values
}

func (values sourceValues) record(name string, expression ast.Expr) {
	if value, ok := stringValue(expression, values.strings); ok {
		values.strings[name] = value
		return
	}
	if value, ok := stringSliceValue(expression, values.strings, values.slices); ok {
		values.slices[name] = value
	}
}

func checkCall(path string, fileSet *token.FileSet, call *ast.CallExpr, imports map[string]importedPackage, values sourceValues, httpImportFound bool) []Finding {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok {
		return nil
	}
	imported, ok := imports[packageName.Name]
	if !ok {
		return nil
	}
	line := fileSet.Position(call.Pos()).Line
	if imported.path == "net/http" {
		if httpImportFound {
			return nil
		}
		return []Finding{{Path: path, Line: line, Rule: "http client"}}
	}
	if imported.path == "net" && isNetworkCall(selector.Sel.Name) {
		return []Finding{{Path: path, Line: line, Rule: "socket listener/dialer"}}
	}
	if isCredentialPackage(imported.path) {
		return []Finding{{Path: path, Line: line, Rule: "credential API"}}
	}
	if imported.path == "os" && selector.Sel.Name == "StartProcess" {
		return []Finding{{Path: path, Line: line, Rule: "process spawn"}}
	}
	if imported.path == "syscall" && (selector.Sel.Name == "Exec" || selector.Sel.Name == "ForkExec") {
		return []Finding{{Path: path, Line: line, Rule: "process spawn"}}
	}
	if imported.path != "os/exec" || selector.Sel.Name != "Command" && selector.Sel.Name != "CommandContext" {
		return nil
	}
	commandIndex := 0
	if selector.Sel.Name == "CommandContext" {
		commandIndex = 1
	}
	command, args, resolved := commandArguments(call, commandIndex, values)
	if !resolved || !isLiteralString(call.Args[commandIndex]) {
		if allowsDynamicExec(path) {
			return nil
		}
		return []Finding{{Path: path, Line: line, Rule: "dynamic exec"}}
	}
	base := strings.ToLower(filepath.Base(command))
	if base == "git" && containsAny(args, gitNetworkMutations) {
		return []Finding{{Path: path, Line: line, Rule: "git network mutation"}}
	}
	if isShell(base) && containsAny(args, shellInterpretationFlags) {
		return []Finding{{Path: path, Line: line, Rule: "shell interpreter"}}
	}
	if isPublishingCommand(base, args) {
		return []Finding{{Path: path, Line: line, Rule: "deployment/publishing command"}}
	}
	return nil
}

func allowsDynamicExec(path string) bool {
	normalized := filepath.ToSlash(path)
	for allowed := range dynamicExecAllowlist {
		if normalized == allowed || strings.HasSuffix(normalized, "/"+allowed) {
			return true
		}
	}
	return false
}

var gitNetworkMutations = map[string]struct{}{
	"clone": {}, "fetch": {}, "ls-remote": {}, "pull": {}, "push": {},
}

var shellInterpretationFlags = map[string]struct{}{
	"-c": {}, "/c": {}, "-command": {}, "/command": {},
}

var publishingCommands = map[string]struct{}{
	"deploy": {}, "deployment": {}, "helm": {}, "netlify": {}, "publish": {},
	"release": {}, "terraform": {}, "vercel": {},
}

func isNetworkCall(name string) bool {
	switch name {
	case "Dial", "DialIP", "DialTCP", "DialTimeout", "DialUDP", "Listen", "ListenPacket", "ListenTCP", "ListenUDP", "ListenUnix", "ListenUnixgram":
		return true
	default:
		return false
	}
}

func isCredentialPackage(path string) bool {
	if path == "os/user" || path == "github.com/zalando/go-keyring" || path == "github.com/keybase/go-keychain" {
		return true
	}
	lower := strings.ToLower(path)
	return strings.Contains(lower, "/oauth2") || strings.Contains(lower, "/credential") || strings.Contains(lower, "/keyring")
}

func isShell(command string) bool {
	switch command {
	case "bash", "cmd", "fish", "powershell", "pwsh", "sh", "zsh":
		return true
	default:
		return false
	}
}

func isPublishingCommand(command string, args []string) bool {
	if _, ok := publishingCommands[command]; ok {
		return true
	}
	if command == "kubectl" || command == "oc" {
		return containsAny(args, map[string]struct{}{"apply": {}, "create": {}, "delete": {}, "patch": {}, "replace": {}, "rollout": {}, "set": {}})
	}
	if command == "npm" || command == "yarn" || command == "pnpm" {
		return containsAny(args, map[string]struct{}{"publish": {}, "release": {}})
	}
	return false
}

func containsAny(values []string, wanted map[string]struct{}) bool {
	for _, value := range values {
		if _, ok := wanted[strings.ToLower(value)]; ok {
			return true
		}
	}
	return false
}

func commandArguments(call *ast.CallExpr, commandIndex int, values sourceValues) (string, []string, bool) {
	if commandIndex < 0 || len(call.Args) <= commandIndex {
		return "", nil, false
	}
	command, ok := stringValue(call.Args[commandIndex], values.strings)
	if !ok {
		return "", nil, false
	}
	arguments := call.Args[commandIndex+1:]
	var args []string
	for index, expression := range arguments {
		if index == len(arguments)-1 && call.Ellipsis.IsValid() {
			slice, sliceOK := stringSliceValue(expression, values.strings, values.slices)
			if !sliceOK {
				return "", nil, false
			}
			args = append(args, slice...)
			continue
		}
		value, valueOK := stringValue(expression, values.strings)
		if !valueOK {
			return "", nil, false
		}
		args = append(args, value)
	}
	return command, args, true
}

func isLiteralString(expression ast.Expr) bool {
	literal, ok := expression.(*ast.BasicLit)
	return ok && literal.Kind == token.STRING
}

func stringValue(expression ast.Expr, known map[string]string) (string, bool) {
	switch current := expression.(type) {
	case *ast.BasicLit:
		if current.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(current.Value)
		return value, err == nil
	case *ast.Ident:
		value, ok := known[current.Name]
		return value, ok
	default:
		return "", false
	}
}

func stringSliceValue(expression ast.Expr, knownStrings map[string]string, knownSlices map[string][]string) ([]string, bool) {
	if identifier, ok := expression.(*ast.Ident); ok {
		value, exists := knownSlices[identifier.Name]
		return value, exists
	}
	composite, ok := expression.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(composite.Elts))
	for _, element := range composite.Elts {
		value, ok := stringValue(element, knownStrings)
		if !ok {
			return nil, false
		}
		result = append(result, value)
	}
	return result, true
}
