package plugconf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pkg/errors"

	multierror "github.com/hashicorp/go-multierror"
	"github.com/vim-volt/volt/httputil"
	"github.com/vim-volt/volt/lockjson"
	"github.com/vim-volt/volt/pathutil"

	"github.com/haya14busa/go-vimlparser"
	"github.com/haya14busa/go-vimlparser/ast"
	"github.com/haya14busa/go-vimlparser/token"
)

type loadOnType string

// TODO: make this uint
const (
	loadOnStart    loadOnType = "(loadOnStart)"
	loadOnFileType            = "FileType"
	loadOnExcmd               = "(loadOnExcmd)"
)

const (
	// TODO: Check duplicate variable for excmdLoadPlugin
	excmdLoadPlugin   = "s:__volt_excmd_load_plugin"
	lazyLoadExcmdFunc = "s:__volt_lazy_load_excmd"
	completeFunc      = "s:__volt_complete"
)

func isProhibitedFuncName(name string) bool {
	return name == lazyLoadExcmdFunc ||
		name == completeFunc
}

// ParsedInfo represents parsed info of plugconf.
type ParsedInfo struct {
	reposID             int
	reposPath           pathutil.ReposPath
	functions           []string
	onLoadPreFunc       string
	onLoadPostFunc      string
	loadConditionFunc   string
	loadUnconditionally bool
	loadOnFunc          string
	loadOn              loadOnType
	loadOnArg           string
	dependsFunc         string
	depends             pathutil.ReposPathList
}

// ConvertConfigToOnLoadPreFunc converts s:config() function name to
// s:on_load_pre() (see 'volt migrate plugconf/config-func' function).
// If no s:config() function is found, returns false.
// If found, returns true.
func (pi *ParsedInfo) ConvertConfigToOnLoadPreFunc() bool {
	newStr := rxFuncName.ReplaceAllString(pi.onLoadPreFunc, "${1}on_load_pre")
	if pi.onLoadPreFunc != newStr {
		return false
	}
	pi.onLoadPreFunc = newStr
	return true
}

// GeneratePlugconf generates a plugconf file placed at
// "$VOLTPATH/plugconf/{repos}.vim".
func (pi *ParsedInfo) GeneratePlugconf() ([]byte, error) {
	// Merge result and return it
	var buf bytes.Buffer

	// modeline
	buf.WriteString("\" vim:et:sw=2:ts=2\n\n")

	// s:on_load_pre()
	if pi.onLoadPreFunc != "" {
		buf.WriteString(pi.onLoadPreFunc)
	} else {
		buf.WriteString(skeletonPlugconfOnLoadPre)
	}
	buf.WriteString("\n\n")

	// s:on_load_post()
	if pi.onLoadPostFunc != "" {
		buf.WriteString(pi.onLoadPostFunc)
	} else {
		buf.WriteString(skeletonPlugconfOnLoadPost)
	}
	buf.WriteString("\n\n")

	// s:should_load()
	if pi.loadConditionFunc != "" {
		buf.WriteString(pi.loadConditionFunc)
	} else {
		buf.WriteString(skeletonPlugconfShouldLoad)
	}
	buf.WriteString("\n\n")

	// s:loaded_on()
	if pi.loadOnFunc != "" {
		buf.WriteString(pi.loadOnFunc)
	} else {
		buf.WriteString(skeletonPlugconfLoadOn)
	}
	buf.WriteString("\n\n")

	// s:depends()
	if pi.dependsFunc != "" {
		buf.WriteString(pi.dependsFunc)
	} else {
		buf.WriteString(skeletonPlugconfDepends)
	}

	for _, f := range pi.functions {
		buf.WriteString("\n\n")
		buf.WriteString(f)
	}

	return buf.Bytes(), nil
}

// ParseError does not provide Error() because I don't want let it pretend like
// error type. Receivers of a value of this type must decide how to handle.
type ParseError struct {
	path  string
	merr  *multierror.Error
	mwarn *multierror.Error
}

func newParseError(path string) *ParseError {
	var e ParseError
	e.path = path
	e.merr = newParseErrorMultiError("parse errors in ", path)
	e.mwarn = newParseErrorMultiError("parse warnings in ", path)
	return &e
}

func newParseErrorMultiError(prefix, path string) *multierror.Error {
	return &multierror.Error{
		Errors: make([]error, 0, 8),
		ErrorFormat: func(errs []error) string {
			var buf bytes.Buffer
			buf.WriteString(prefix)
			buf.WriteString(path)
			buf.WriteString(":")
			for _, e := range errs {
				buf.WriteString("\n* ")
				buf.WriteString(e.Error())
			}
			return buf.String()
		},
	}
}

// HasErrsOrWarns returns true when 1 or more errors or warnings.
func (e *ParseError) HasErrsOrWarns() bool {
	return e != nil && (e.merr.ErrorOrNil() != nil || e.mwarn.ErrorOrNil() != nil)
}

// HasErrs returns true when 1 or more errors.
func (e *ParseError) HasErrs() bool {
	return e != nil && e.merr.ErrorOrNil() != nil
}

// HasWarns returns true when 1 or more warnings.
func (e *ParseError) HasWarns() bool {
	return e != nil && e.mwarn.ErrorOrNil() != nil
}

// Errors returns multierror.Error of errors.
func (e *ParseError) Errors() *multierror.Error {
	if e == nil {
		return nil
	}
	return e.mwarn
}

// ErrorsAndWarns returns multierror.Error which errors and warnings are mixed in.
func (e *ParseError) ErrorsAndWarns() *multierror.Error {
	if e == nil {
		return nil
	}
	var result *multierror.Error
	if e.merr.ErrorOrNil() != nil {
		result = multierror.Append(result, e.merr.Errors...)
	}
	if e.mwarn.ErrorOrNil() != nil {
		result = multierror.Append(result, e.mwarn.Errors...)
	}
	return result
}

func (e *ParseError) merge(e2 *ParseError) {
	if e == nil || e2 == nil {
		return
	}
	if e2.merr.ErrorOrNil() != nil {
		e.merr = multierror.Append(e.merr, e2.merr.Errors...)
	}
	if e2.mwarn.ErrorOrNil() != nil {
		e.mwarn = multierror.Append(e.mwarn, e2.mwarn.Errors...)
	}
}

// MultiParseError holds multiple ParseError.
type MultiParseError []ParseError

// HasErrs returns true when any of holding ParseError.HasErrs() returns true.
func (errs MultiParseError) HasErrs() bool {
	for _, e := range errs {
		if e.HasErrs() {
			return true
		}
	}
	return false
}

// HasWarns returns true when any of holding ParseError.HasWarns() returns true.
func (errs MultiParseError) HasWarns() bool {
	for _, e := range errs {
		if e.HasWarns() {
			return true
		}
	}
	return false
}

// Errors returns all errors in holding ParseError.
func (errs MultiParseError) Errors() *multierror.Error {
	return errs.concatErrors(false)
}

// Warns returns all warnings in holding ParseError.
func (errs MultiParseError) Warns() *multierror.Error {
	return errs.concatErrors(true)
}

func (errs MultiParseError) concatErrors(showWarns bool) *multierror.Error {
	result := &multierror.Error{
		Errors: make([]error, 0, len(errs)),
		ErrorFormat: func(errs []error) string {
			var buf bytes.Buffer
			for _, e := range errs {
				buf.WriteString(e.Error())
				buf.WriteString("\n")
			}
			return buf.String()
		},
	}
	for _, e := range errs {
		merr := e.merr
		if showWarns {
			merr = e.mwarn
		}
		if merr.ErrorOrNil() != nil {
			// Call merr.Error() to apply error format func
			result = multierror.Append(result, errors.New(merr.Error()))
		}
	}
	return result
}

// ErrorsAndWarns returns errors and warnings in holding ParseError.
func (errs MultiParseError) ErrorsAndWarns() *multierror.Error {
	var result *multierror.Error
	for _, e := range errs {
		merr := e.ErrorsAndWarns()
		if merr.ErrorOrNil() != nil {
			result = multierror.Append(result, merr.Errors...)
		}
	}
	return result
}

func (errs MultiParseError) merge(otherErrs MultiParseError) MultiParseError {
Main:
	for _, err := range otherErrs {
		for _, e := range errs {
			if e.path == err.path {
				e.merge(&err)
				continue Main
			}
		}
		errs = append(errs, err)
	}
	return errs
}

// ParsePlugconfFile parses plugconf and returns parsed info and parse error and
// warnings.
// path is a filepath of plugconf.
// reposID is an ID of unsigned integer which identifies one plugconf.
// reposPath is an pathutil.ReposPath of plugconf.
func ParsePlugconfFile(path string, reposID int, reposPath pathutil.ReposPath) (result *ParsedInfo, parseErr *ParseError) {
	// this function always returns non-nil parseErr
	// (which may have empty errors / warns)
	parseErr = new(ParseError)

	content, err := ioutil.ReadFile(path)
	if err != nil {
		err = multierror.Append(nil, err)
		return
	}
	file, err := vimlparser.ParseFile(bytes.NewReader(content), path, nil)
	if err != nil {
		err = multierror.Append(nil, err)
		return
	}
	result, parseErr = ParsePlugconf(file, content, path)
	if result != nil {
		result.reposID = reposID
		result.reposPath = reposPath
	}
	return
}

// ParsePlugconf always returns non-nil parseErr
// (which may have empty errors / warns)
func ParsePlugconf(file *ast.File, src []byte, path string) (*ParsedInfo, *ParseError) {
	var loadOn = loadOnStart
	var loadOnArg string
	var loadOnFunc string
	var onLoadPreFunc string
	var onLoadPostFunc string
	var loadConditionFunc string
	var loadUnconditionally bool
	var functions []string
	var dependsFunc string
	var depends pathutil.ReposPathList

	parseErr := newParseError(path)

	// Inspect nodes and get above values from plugconf script
	ast.Inspect(file, func(node ast.Node) bool {
		// Cast to function node (return if it's not a function node)
		fn, ok := node.(*ast.Function)
		if !ok {
			return true
		}

		// Get function name
		ident, ok := fn.Name.(*ast.Ident)
		if !ok {
			return true
		}

		switch {
		case ident.Name == "s:loaded_on":
			if loadOnFunc != "" {
				parseErr.merr = multierror.Append(parseErr.merr,
					errors.New("duplicate s:loaded_on()"))
				return true
			}
			if !isEmptyFunc(fn) {
				loadOnFunc = string(extractBody(fn, src))
				var err error
				loadOn, loadOnArg, err = inspectReturnValue(fn)
				if err != nil {
					parseErr.merr = multierror.Append(parseErr.merr, err)
				}
			}
		case ident.Name == "s:config":
			if onLoadPreFunc != "" {
				parseErr.merr = multierror.Append(parseErr.merr,
					errors.New("duplicate s:on_load_pre() and s:config()"))
				return true
			}
			parseErr.mwarn = multierror.Append(parseErr.mwarn,
				errors.New("s:config() is deprecated. "+
					"please use s:on_load_pre() instead, or run "+
					"\"volt migrate plugconf/config-func\" to rewrite existing plugconf files"))
			if !isEmptyFunc(fn) {
				onLoadPreFunc = string(extractBody(fn, src))
				onLoadPreFunc = rxFuncName.ReplaceAllString(
					onLoadPreFunc, "${1}on_load_pre",
				)
			}
		case ident.Name == "s:on_load_pre":
			if onLoadPreFunc != "" {
				parseErr.merr = multierror.Append(parseErr.merr,
					errors.New("duplicate s:on_load_pre() and s:config()"))
				return true
			}
			if !isEmptyFunc(fn) {
				onLoadPreFunc = string(extractBody(fn, src))
			}
		case ident.Name == "s:on_load_post":
			if onLoadPostFunc != "" {
				parseErr.merr = multierror.Append(parseErr.merr,
					errors.New("duplicate s:on_load_post()"))
				return true
			}
			if !isEmptyFunc(fn) {
				onLoadPostFunc = string(extractBody(fn, src))
			}
		case ident.Name == "s:should_load":
			if loadConditionFunc != "" {
				parseErr.merr = multierror.Append(parseErr.merr,
					errors.New("duplicate s:should_load()"))
				return true
			}
			if !isEmptyFunc(fn) {
				loadConditionFunc = string(extractBody(fn, src))
				loadUnconditionally = shouldLoadUnconditionally(fn)
			}
		case ident.Name == "s:depends":
			if dependsFunc != "" {
				parseErr.merr = multierror.Append(parseErr.merr,
					errors.New("duplicate s:depends()"))
				return true
			}
			if !isEmptyFunc(fn) {
				dependsFunc = string(extractBody(fn, src))
				var err error
				depends, err = getDependencies(fn)
				if err != nil {
					parseErr.merr = multierror.Append(parseErr.merr, err)
				}
			}
		case isProhibitedFuncName(ident.Name):
			parseErr.merr = multierror.Append(parseErr.merr,
				errors.Errorf(
					"'%s' is prohibited function name. please use other function name", ident.Name))
		default:
			functions = append(functions, string(extractBody(fn, src)))
		}

		return true
	})

	if parseErr.HasErrs() {
		return nil, parseErr
	}

	return &ParsedInfo{
		functions:           functions,
		onLoadPreFunc:       onLoadPreFunc,
		onLoadPostFunc:      onLoadPostFunc,
		loadConditionFunc:   loadConditionFunc,
		loadUnconditionally: loadUnconditionally,
		loadOnFunc:          loadOnFunc,
		loadOn:              loadOn,
		loadOnArg:           loadOnArg,
		dependsFunc:         dependsFunc,
		depends:             depends,
	}, parseErr
}

// Inspect return value of s:loaded_on() function in plugconf
func inspectReturnValue(fn *ast.Function) (loadOnType, string, error) {
	var loadOn loadOnType
	var loadOnArg string
	var err error
	ast.Inspect(fn, func(node ast.Node) bool {
		// Cast to return node (return if it's not a return node)
		ret, ok := node.(*ast.Return)
		if !ok {
			return true
		}

		// Parse the argument of :return
		rhs, ok := ret.Result.(*ast.BasicLit)
		if ok && rhs.Kind == token.STRING {
			value := rhs.Value[1 : len(rhs.Value)-1]
			if value == "start" {
				loadOn = loadOnStart
			} else if strings.HasPrefix(value, "filetype=") {
				loadOn = loadOnFileType
				loadOnArg = strings.TrimPrefix(value, "filetype=")
			} else if strings.HasPrefix(value, "excmd=") {
				loadOn = loadOnExcmd
				loadOnArg = strings.TrimPrefix(value, "excmd=")
			} else {
				err = errors.New("Invalid rhs of ':return': " + rhs.Value)
			}
		}

		return true
	})
	if string(loadOn) == "" {
		return "", "", errors.New("can't detect return value of s:loaded_on()")
	}
	return loadOn, loadOnArg, err
}

// Returns true if fn.Body is empty or has only comment nodes
func isEmptyFunc(fn *ast.Function) bool {
	for i := range fn.Body {
		empty := true
		ast.Inspect(fn.Body[i], func(node ast.Node) bool {
			if _, ok := node.(*ast.Comment); ok {
				return true
			}
			empty = false
			return false
		})
		if !empty {
			return false
		}
	}
	return true
}

func extractBody(fn *ast.Function, src []byte) []byte {
	pos := fn.Pos()

	endpos := fn.EndFunction.Pos()
	endfunc := fn.EndFunction.ExArg
	cmdlen := endfunc.Argpos.Offset - endfunc.Cmdpos.Offset
	endpos.Offset += cmdlen

	return src[pos.Offset:endpos.Offset]
}

func getDependencies(fn *ast.Function) (pathutil.ReposPathList, error) {
	var deps pathutil.ReposPathList
	var parseErr error

	ast.Inspect(fn, func(node ast.Node) bool {
		// Cast to return node (return if it's not a return node)
		ret, ok := node.(*ast.Return)
		if !ok {
			return true
		}
		if list, ok := ret.Result.(*ast.List); ok {
			for i := range list.Values {
				if str, ok := list.Values[i].(*ast.BasicLit); ok {
					if deps == nil {
						deps = make(pathutil.ReposPathList, 0, len(list.Values))
					}
					if str.Kind == token.STRING {
						reposPath, err := pathutil.NormalizeRepos(str.Value[1 : len(str.Value)-1])
						if err != nil {
							parseErr = err
							return false
						}
						deps = append(deps, reposPath)
					}
				}
			}
		}
		return true
	})

	return deps, parseErr
}

// shouldLoadUnconditionally checks if the given function only contains the
// single default 'return 1' statement which means loading unconditionally.
// If any other statement is found it is considered a condition.
func shouldLoadUnconditionally(fn *ast.Function) bool {
	for _, stmt := range fn.Body {
		switch stmt.(type) {
		case *ast.Comment:
			// comment nodes are ignored
		case *ast.Return:
			returnExpr := stmt.(*ast.Return).Result
			val, ok := returnExpr.(*ast.BasicLit)
			if ok && (val.Value == "1" || val.Value == "v:true") {
				// "return 1" or "return v:true" is the default and means unconditional execution
				return true
			} else {
				// any other return value is considered a condition
				return false
			}
		default:
			// any other statement is considered a condition
			return false
		}
	}

	// no statement is considered unconditional execution
	return true
}

// rxFuncName is a pattern which matches to function name.
// Note that $2 is a function name.
// $1 is a string before a function name.
var rxFuncName = regexp.MustCompile(`\A(fu\w+!?\s+s:)(\w+)`)

func convertToDecodableFunc(funcBody string, reposPath pathutil.ReposPath, reposID int) string {
	// Change function name (e.g. s:loaded_on() -> s:loaded_on_1())
	funcBody = rxFuncName.ReplaceAllString(funcBody, fmt.Sprintf("${1}${2}_%d", reposID))
	// Add repos path as comment
	funcBody = "\" " + reposPath.String() + "\n" + funcBody
	return funcBody
}

// ParseMultiPlugconf parses plugconfs of given reposList.
func ParseMultiPlugconf(reposList []lockjson.Repos) (*MultiParsedInfo, MultiParseError) {
	plugconfMap, parseErr := parsePlugconfAsMap(reposList)
	if parseErr.HasErrs() {
		return nil, parseErr
	}
	parseErr = parseErr.merge(sortByDepends(reposList, plugconfMap))
	if parseErr.HasErrs() {
		return nil, parseErr
	}
	return &MultiParsedInfo{
		plugconfMap: plugconfMap,
		reposList:   reposList,
	}, parseErr
}

type parsedInfoMap map[pathutil.ReposPath]*ParsedInfo

// MultiParsedInfo holds multiple ParsedInfo.
// This value is generated by ParseMultiPlugconf.
type MultiParsedInfo struct {
	plugconfMap parsedInfoMap
	reposList   []lockjson.Repos
}

// GenerateBundlePlugconf generates bundled plugconf content.
// Generated content does not include s:loaded_on() function.
// vimrcPath and gvimrcPath are fullpath of vimrc and gvimrc.
// They become an empty string when each path does not exist.
func (mp *MultiParsedInfo) GenerateBundlePlugconf(vimrcPath, gvimrcPath string) ([]byte, error) {
	functions := make([]string, 0, 64)
	loadCmds := make([]string, 0, len(mp.reposList))
	lazyExcmd := make(map[string]string, len(mp.reposList))

	for _, repos := range mp.reposList {
		p, hasPlugconf := mp.plugconfMap[repos.Path]
		// :packadd <repos>
		optName := filepath.Base(repos.Path.EncodeToPlugDirName())
		packadd := fmt.Sprintf("packadd %s", optName)

		// s:should_load(), s:on_load_pre(), invoked command, s:on_load_post()
		var invokedCmd string
		if hasPlugconf {
			cmds := make([]string, 0, 5)
			if p.onLoadPreFunc != "" {
				functions = append(functions, convertToDecodableFunc(p.onLoadPreFunc, p.reposPath, p.reposID))
				cmds = append(cmds, fmt.Sprintf("call s:on_load_pre_%d()", p.reposID))
			}
			cmds = append(cmds, packadd)
			if p.onLoadPostFunc != "" {
				functions = append(functions, convertToDecodableFunc(p.onLoadPostFunc, p.reposPath, p.reposID))
				cmds = append(cmds, fmt.Sprintf("call s:on_load_post_%d()", p.reposID))
			}
			if p.loadConditionFunc != "" && !p.loadUnconditionally {
				functions = append(functions, convertToDecodableFunc(p.loadConditionFunc, p.reposPath, p.reposID))
				cmds = append([]string{fmt.Sprintf("if s:should_load_%d()", p.reposID)}, cmds...)
				cmds = append(cmds, "endif")
			}
			invokedCmd = strings.Join(cmds, " | ")
		} else {
			invokedCmd = packadd
		}

		// Bootstrap statements
		switch {
		case !hasPlugconf || p.loadOn == loadOnStart:
			loadCmds = append(loadCmds, "  "+invokedCmd)
		case p.loadOn == loadOnFileType:
			loadCmds = append(loadCmds,
				fmt.Sprintf("  autocmd %s %s %s", string(p.loadOn), p.loadOnArg, invokedCmd))
		case p.loadOn == loadOnExcmd:
			// Define dummy Ex commands
			for _, excmd := range strings.Split(p.loadOnArg, ",") {
				lazyExcmd[excmd] = invokedCmd
				loadCmds = append(loadCmds,
					fmt.Sprintf("  command -complete=customlist,%[1]s -bang -bar -range -nargs=* %[3]s call %[2]s('%[3]s', <q-args>, expand('<bang>'), expand('<line1>'), expand('<line2>'))", completeFunc, lazyLoadExcmdFunc, excmd))
			}
		}

		// User defined functions in plugconf
		if hasPlugconf {
			functions = append(functions, p.functions...)
		}
	}

	var buf bytes.Buffer
	buf.WriteString(`if exists('g:loaded_volt_system_bundled_plugconf')
  finish
endif
let g:loaded_volt_system_bundled_plugconf = 1`)
	if len(functions) > 0 {
		buf.WriteString("\n\n")
		buf.WriteString(strings.Join(functions, "\n\n"))
	}
	if len(lazyExcmd) > 0 {
		lazyExcmdJSON, err := json.Marshal(lazyExcmd)
		if err != nil {
			return nil, err
		}
		// * dein#autoload#_on_cmd()
		//   https://github.com/Shougo/dein.vim/blob/2adba7655b23f2fc1ddcd35e15d380c5069a3712/autoload/dein/autoload.vim#L157-L175
		// * dein#autoload#_dummy_complete()
		//   https://github.com/Shougo/dein.vim/blob/2adba7655b23f2fc1ddcd35e15d380c5069a3712/autoload/dein/autoload.vim#L216-L232
		buf.WriteString(`

let ` + excmdLoadPlugin + ` = ` + string(lazyExcmdJSON) + `

function ` + lazyLoadExcmdFunc + `(command, args, bang, line1, line2) abort
  if exists(':' . a:command) is# 2
    execute 'delcommand' a:command
  endif
  execute get(` + excmdLoadPlugin + `, a:command, '')
  if exists(':' . a:command) isnot# 2
    echohl ErrorMsg
    echomsg printf('[volt] Lazy loading of Ex command ''%s'' failed: ''%s'' is not found', a:command, a:command)
    echohl None
    return
  endif
  let range = (a:line1 is# a:line2) ? '' :
        \ (a:line1 is# line("'<") && a:line2 is# line("'>")) ?
        \ "'<,'>" : a:line1 . ',' . a:line2
  try
    execute range . a:command . a:bang a:args
  catch /^Vim\%((\a\+)\)\=:E481/
    " E481: No range allowed
    execute a:command . a:bang a:args
  endtry
endfunction

function ` + completeFunc + `(arglead, cmdline, cursorpos) abort
  let command = matchstr(a:cmdline, '\h\w*')
  if exists(':' . command) is# 2
    execute 'delcommand' command
  endif
  execute get(` + excmdLoadPlugin + `, command, '')
  if exists(':' . command) is# 2
    call feedkeys("\<C-d>", 'n')
  endif
  return [a:arglead]
endfunction
`)
	}
	if len(loadCmds) > 0 {
		buf.WriteString("\n\n")
		buf.WriteString(`augroup volt-bundled-plugconf
  autocmd!
`)
		buf.WriteString(strings.Join(loadCmds, "\n"))
		buf.WriteString("\naugroup END")
	}

	if vimrcPath != "" || gvimrcPath != "" {
		buf.WriteString("\n")
		if vimrcPath != "" {
			buf.WriteString("\n")
			vimrcPath = strings.Replace(vimrcPath, "'", "''", -1)
			buf.WriteString("let $MYVIMRC = '" + vimrcPath + "'")
		}
		if gvimrcPath != "" {
			buf.WriteString("\n")
			gvimrcPath = strings.Replace(gvimrcPath, "'", "''", -1)
			buf.WriteString("let $MYGVIMRC = '" + gvimrcPath + "'")
		}
	}

	return buf.Bytes(), nil
}

// Each iterates each repository by given func.
func (mp *MultiParsedInfo) Each(f func(pathutil.ReposPath, *ParsedInfo)) {
	for reposPath, info := range mp.plugconfMap {
		f(reposPath, info)
	}
}

// RdepsOf returns depended (required) plugins of reposPath.
// reposList is used to calculate dependency of reposPath.
func RdepsOf(reposPath pathutil.ReposPath, reposList []lockjson.Repos) (pathutil.ReposPathList, error) {
	plugconfMap, parseErr := parsePlugconfAsMap(reposList)
	if parseErr.HasErrs() {
		return nil, parseErr.ErrorsAndWarns()
	}
	_, _, rdepsMap := getDepMaps(reposList, plugconfMap)
	rdeps := rdepsMap[reposPath]
	if rdeps == nil {
		rdeps = make(pathutil.ReposPathList, 0)
	}
	return rdeps, nil
}

// Parse plugconf of reposList and return parsed plugconf info as map
func parsePlugconfAsMap(reposList []lockjson.Repos) (map[pathutil.ReposPath]*ParsedInfo, MultiParseError) {
	parseErrAll := make(MultiParseError, 0, len(reposList))
	plugconfMap := make(parsedInfoMap, len(reposList))
	reposID := 1
	for _, repos := range reposList {
		path := repos.Path.Plugconf()
		if !pathutil.Exists(path) {
			continue
		}
		result, parseErr := ParsePlugconfFile(path, reposID, repos.Path)
		if parseErr.HasErrsOrWarns() {
			parseErrAll = append(parseErrAll, *parseErr)
		}
		if result != nil {
			plugconfMap[repos.Path] = result
			reposID++
		}
	}
	return plugconfMap, parseErrAll
}

type visitingStatus int

const (
	notVisited visitingStatus = iota
	visiting
	visited
)

// Move the plugins which was depended to previous plugin which depends to them.
// reposList is sorted in-place.
func sortByDepends(reposList []lockjson.Repos, plugconfMap map[pathutil.ReposPath]*ParsedInfo) (parseErrAll MultiParseError) {
	visitedStatusMap := make(map[pathutil.ReposPath]visitingStatus, len(reposList))
	order := make(map[pathutil.ReposPath]int, len(reposList))
	for i := range reposList {
		if visitedStatusMap[reposList[i].Path] == notVisited {
			errors := visitRepos([]pathutil.ReposPath{reposList[i].Path}, reposList, plugconfMap, visitedStatusMap, order)
			parseErrAll = append(parseErrAll, errors...)
		}
	}
	if parseErrAll.HasErrs() {
		return
	}

	sort.Slice(reposList, func(i, j int) bool {
		return order[reposList[i].Path] < order[reposList[j].Path]
	})
	return
}

func visitRepos(dependedReposList []pathutil.ReposPath, reposList []lockjson.Repos, plugconfMap map[pathutil.ReposPath]*ParsedInfo, visitedStatusMap map[pathutil.ReposPath]visitingStatus, order map[pathutil.ReposPath]int) (parseErrAll MultiParseError) {
	reposPath := dependedReposList[len(dependedReposList)-1]
	p, exists := plugconfMap[reposPath]
	if !exists {
		return
	}
	parseErr := newParseError(reposPath.Plugconf())
	visitedStatusMap[reposPath] = visiting
Main:
	for _, dep := range p.depends {
		switch visitedStatusMap[dep] {
		case visiting:
			str := formatCircularDependency(dependedReposList, dep)
			parseErr.merr = multierror.Append(parseErr.merr,
				errors.Errorf("s:depends(): circular dependency: %s", str))
		case notVisited:
			for _, repos := range reposList {
				if repos.Path == dep {
					errs := visitRepos(append(dependedReposList, dep), reposList, plugconfMap, visitedStatusMap, order)
					parseErrAll = append(parseErrAll, errs...)
					continue Main
				}
			}
			parseErr.merr = multierror.Append(parseErr.merr,
				errors.Errorf("s:depends(): missing plugin: %s", dep))
		}
	}
	if parseErr.HasErrsOrWarns() {
		parseErrAll = append(parseErrAll, *parseErr)
	}
	visitedStatusMap[reposPath] = visited
	order[reposPath] = len(order)
	return
}

func formatCircularDependency(dependedReposList []pathutil.ReposPath, head pathutil.ReposPath) string {
	var graph []pathutil.ReposPath
	for i, path := range dependedReposList {
		if path == head {
			graph = dependedReposList[i:]
			break
		}
	}
	graphStr := string(graph[0])
	for _, path := range graph[1:] {
		graphStr += " => " + string(path)
	}
	return graphStr
}

func getDepMaps(reposList []lockjson.Repos, plugconfMap map[pathutil.ReposPath]*ParsedInfo) (map[pathutil.ReposPath]*lockjson.Repos, map[pathutil.ReposPath]pathutil.ReposPathList, map[pathutil.ReposPath]pathutil.ReposPathList) {
	reposMap := make(map[pathutil.ReposPath]*lockjson.Repos, len(reposList))
	depsMap := make(map[pathutil.ReposPath]pathutil.ReposPathList, len(reposList))
	rdepsMap := make(map[pathutil.ReposPath]pathutil.ReposPathList, len(reposList))
	for i := range reposList {
		reposPath := reposList[i].Path
		reposMap[reposPath] = &reposList[i]
		if p, exists := plugconfMap[reposPath]; exists {
			depsMap[reposPath] = p.depends
			for _, dep := range p.depends {
				rdepsMap[dep] = append(rdepsMap[dep], reposPath)
			}
		}
	}
	return reposMap, depsMap, rdepsMap
}

// Template is a content of plugconf template.
type Template struct {
	template []byte
}

// FetchPlugconfTemplate fetches reposPath's plugconf from vim-volt/plugconf-templates
// repository.
// Fetched URL: https://raw.githubusercontent.com/vim-volt/plugconf-templates/master/templates/{reposPath}.vim
func FetchPlugconfTemplate(reposPath pathutil.ReposPath) (*Template, error) {
	url := path.Join("https://raw.githubusercontent.com/vim-volt/plugconf-templates/master/templates", reposPath.String()+".vim")
	content, err := httputil.GetContent(url)
	if err != nil {
		return nil, err
	}
	return &Template{content}, nil
}

const skeletonPlugconfOnLoadPre = `" Plugin configuration like the code written in vimrc.
" This configuration is executed *before* a plugin is loaded.
function! s:on_load_pre()
endfunction`

const skeletonPlugconfOnLoadPost = `" Plugin configuration like the code written in vimrc.
" This configuration is executed *after* a plugin is loaded.
function! s:on_load_post()
endfunction`

const skeletonPlugconfShouldLoad = `" Whether to actually load this plugin.
" Return a 0 value to prohibit loading the plugin. All other numbers mean
" this plugin will be loaded.
function! s:should_load()
  return 1
endfunction`

const skeletonPlugconfLoadOn = `" This function determines when a plugin is loaded.
"
" Possible values are:
" * 'start' (a plugin will be loaded at VimEnter event)
" * 'filetype=<filetypes>' (a plugin will be loaded at FileType event)
" * 'excmd=<excmds>' (a plugin will be loaded at CmdUndefined event)
" <filetypes> and <excmds> can be multiple values separated by comma.
"
" This function must contain 'return "<str>"' code.
" (the argument of :return must be string literal)
function! s:loaded_on()
  return 'start'
endfunction`

const skeletonPlugconfDepends = `" Dependencies of this plugin.
" The specified dependencies are loaded after this plugin is loaded.
"
" This function must contain 'return [<repos>, ...]' code.
" (the argument of :return must be list literal, and the elements are string)
" e.g. return ['github.com/tyru/open-browser.vim']
function! s:depends()
  return []
endfunction`

// Generate generates plugconf content from Template.
func (pt *Template) Generate(path string) ([]byte, *multierror.Error) {
	result := &ParsedInfo{}
	if pt != nil {
		// Parse fetched plugconf
		tmpl, err := vimlparser.ParseFile(bytes.NewReader(pt.template), path, nil)
		if err != nil {
			return nil, multierror.Append(nil, err)
		}
		var parseErr *ParseError
		result, parseErr = ParsePlugconf(tmpl, pt.template, path)
		if parseErr.HasErrs() {
			return nil, parseErr.ErrorsAndWarns()
		}
	}
	content, err := result.GeneratePlugconf()
	if err != nil {
		return nil, multierror.Append(nil, err)
	}
	return content, nil
}
