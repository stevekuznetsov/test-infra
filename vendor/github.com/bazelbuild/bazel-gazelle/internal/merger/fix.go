/* Copyright 2017 The Bazel Authors. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package merger

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/internal/config"
	bf "github.com/bazelbuild/buildtools/build"
)

// Much of this file could be simplified by using
// github.com/bazelbuild/buildtools/edit. However, through a transitive
// dependency, that library depends on a proto in Bazel itself, which is
// a 95MB download. Not worth it.

// FixFile updates rules in f that were generated by an older version of
// Gazelle to a newer form that can be merged with freshly generated rules.
//
// If c.ShouldFix is true, FixFile may perform potentially destructive
// transformations, such as squashing or deleting rules (e.g., cgo_library).
// If not, FixFile will perform a set of low-risk transformations (e.g., removing
// unused attributes) and will print a message about transformations it
// would have performed.
//
// FixLoads should be called after this, since it will fix load statements that
// may be broken by transformations applied by this function.
func FixFile(c *config.Config, f *bf.File) {
	migrateLibraryEmbed(c, f)
	migrateGrpcCompilers(c, f)
	removeBinaryImportPath(c, f)
	flattenSrcs(c, f)
	squashCgoLibrary(c, f)
	squashXtest(c, f)
	removeLegacyProto(c, f)
}

// migrateLibraryEmbed converts "library" attributes to "embed" attributes,
// preserving comments. This only applies to Go rules, and only if there is
// no keep comment on "library" and no existing "embed" attribute.
func migrateLibraryEmbed(c *config.Config, f *bf.File) {
	for _, stmt := range f.Stmt {
		call, ok := stmt.(*bf.CallExpr)
		if !ok || shouldKeep(stmt) {
			continue
		}
		rule := bf.Rule{Call: call}
		if !isGoRule(rule.Kind()) {
			continue
		}
		libExpr := rule.Attr("library")
		if libExpr == nil || shouldKeep(libExpr) || rule.Attr("embed") != nil {
			continue
		}
		rule.DelAttr("library")
		rule.SetAttr("embed", &bf.ListExpr{List: []bf.Expr{libExpr}})
	}
}

// migrateGrpcCompilers converts "go_grpc_library" rules into "go_proto_library"
// rules with a "compilers" attribute.
func migrateGrpcCompilers(c *config.Config, f *bf.File) {
	for _, stmt := range f.Stmt {
		call, ok := stmt.(*bf.CallExpr)
		if !ok {
			continue
		}
		rule := bf.Rule{Call: call}
		if rule.Kind() != "go_grpc_library" || shouldKeep(stmt) || rule.Attr("compilers") != nil {
			continue
		}
		rule.SetKind("go_proto_library")
		rule.SetAttr("compilers", &bf.ListExpr{
			List: []bf.Expr{&bf.StringExpr{Value: config.GrpcCompilerLabel}},
		})
	}
}

// removeBinaryImportPath removes "importpath" attributes from "go_binary"
// and "go_test" rules. These are now deprecated.
func removeBinaryImportPath(c *config.Config, f *bf.File) {
	for _, stmt := range f.Stmt {
		call, ok := stmt.(*bf.CallExpr)
		if !ok {
			continue
		}
		rule := bf.Rule{Call: call}
		if rule.Kind() != "go_binary" && rule.Kind() != "go_test" {
			continue
		}
		rule.DelAttr("importpath")
	}
}

// squashCgoLibrary removes cgo_library rules with the default name and
// merges their attributes with go_library with the default name. If no
// go_library rule exists, a new one will be created.
//
// Note that the library attribute is disregarded, so cgo_library and
// go_library attributes will be squashed even if the cgo_library was unlinked.
// MergeFile will remove unused values and attributes later.
func squashCgoLibrary(c *config.Config, f *bf.File) {
	// Find the default cgo_library and go_library rules.
	var cgoLibrary, goLibrary bf.Rule
	cgoLibraryIndex := -1

	for i, stmt := range f.Stmt {
		c, ok := stmt.(*bf.CallExpr)
		if !ok {
			continue
		}
		r := bf.Rule{Call: c}
		if r.Kind() == "cgo_library" && r.Name() == config.DefaultCgoLibName && !shouldKeep(c) {
			if cgoLibrary.Call != nil {
				log.Printf("%s: when fixing existing file, multiple cgo_library rules with default name found", f.Path)
				continue
			}
			cgoLibrary = r
			cgoLibraryIndex = i
			continue
		}
		if r.Kind() == "go_library" && r.Name() == config.DefaultLibName {
			if goLibrary.Call != nil {
				log.Printf("%s: when fixing existing file, multiple go_library rules with default name referencing cgo_library found", f.Path)
				continue
			}
			goLibrary = r
		}
	}

	if cgoLibrary.Call == nil {
		return
	}
	if !c.ShouldFix {
		log.Printf("%s: cgo_library is deprecated. Run 'gazelle fix' to squash with go_library.", f.Path)
		return
	}

	// Delete cgo_library.
	f.Stmt = append(f.Stmt[:cgoLibraryIndex], f.Stmt[cgoLibraryIndex+1:]...)

	// Copy the comments and attributes from cgo_library into go_library. If no
	// go_library exists, create an empty one.
	if goLibrary.Call != nil && shouldKeep(goLibrary.Call) {
		return
	}
	if goLibrary.Call == nil {
		goLibrary.Call = &bf.CallExpr{}
		goLibrary.SetKind("go_library")
		goLibrary.SetAttr("name", &bf.StringExpr{Value: config.DefaultLibName})
		if vis := cgoLibrary.Attr("visibility"); vis != nil {
			goLibrary.SetAttr("visibility", vis)
		}
		f.Stmt = append(f.Stmt, goLibrary.Call)
	}

	goLibrary.DelAttr("embed")
	goLibrary.SetAttr("cgo", &bf.LiteralExpr{Token: "True"})
	goLibrary.Call.Comments.Before = append(goLibrary.Call.Comments.Before, cgoLibrary.Call.Comments.Before...)
	goLibrary.Call.Comments.Suffix = append(goLibrary.Call.Comments.Suffix, cgoLibrary.Call.Comments.Suffix...)
	goLibrary.Call.Comments.After = append(goLibrary.Call.Comments.After, cgoLibrary.Call.Comments.After...)
	for _, key := range []string{"cdeps", "clinkopts", "copts", "data", "deps", "gc_goopts", "srcs"} {
		goLibraryAttr := goLibrary.Attr(key)
		cgoLibraryAttr := cgoLibrary.Attr(key)
		if cgoLibraryAttr == nil {
			continue
		}
		if fixedAttr, err := squashExpr(goLibraryAttr, cgoLibraryAttr); err == nil {
			goLibrary.SetAttr(key, fixedAttr)
		}
	}
}

// squashXtest removes go_test rules with the default external name and merges
// their attributes with a go_test rule with the default internal name. If
// no internal go_test rule exists, a new one will be created (effectively
// renaming the old rule).
func squashXtest(c *config.Config, f *bf.File) {
	// Search for internal and external tests.
	var itest, xtest bf.Rule
	xtestIndex := -1
	for i, stmt := range f.Stmt {
		call, ok := stmt.(*bf.CallExpr)
		if !ok {
			continue
		}
		rule := bf.Rule{Call: call}
		if rule.Kind() != "go_test" {
			continue
		}
		if name := rule.Name(); name == config.DefaultTestName {
			itest = rule
		} else if name == config.DefaultXTestName {
			xtest = rule
			xtestIndex = i
		}
	}

	if xtest.Call == nil || shouldKeep(xtest.Call) || (itest.Call != nil && shouldKeep(itest.Call)) {
		return
	}
	if !c.ShouldFix {
		if itest.Call == nil {
			log.Printf("%s: go_default_xtest is no longer necessary. Run 'gazelle fix' to rename to go_default_test.", f.Path)
		} else {
			log.Printf("%s: go_default_xtest is no longer necessary. Run 'gazelle fix' to squash with go_default_test.", f.Path)
		}
		return
	}

	// If there was no internal test, we can just rename the external test.
	if itest.Call == nil {
		xtest.SetAttr("name", &bf.StringExpr{Value: config.DefaultTestName})
		return
	}

	// Attempt to squash.
	if err := squashRule(xtest.Call, itest.Call, f.Path); err != nil {
		log.Print(err)
		return
	}

	// Delete the external test.
	f.Stmt = append(f.Stmt[:xtestIndex], f.Stmt[xtestIndex+1:]...)

	// Copy comments and attributes from external test to internal test.
	itest.Call.Comments.Before = append(itest.Call.Comments.Before, xtest.Call.Comments.Before...)
	itest.Call.Comments.Suffix = append(itest.Call.Comments.Suffix, xtest.Call.Comments.Suffix...)
	itest.Call.Comments.After = append(itest.Call.Comments.After, xtest.Call.Comments.After...)
}

// squashRule copies information in mergeable attributes from src into dst. This
// works similarly to mergeRule, but it doesn't discard information from dst. It
// detects duplicate elements, but it doesn't sort elements after squashing.
// If squashing fails because the expression is understood, an error is
// returned, and neither rule is modified.
func squashRule(src, dst *bf.CallExpr, filename string) error {
	srcRule := bf.Rule{Call: src}
	dstRule := bf.Rule{Call: dst}
	kind := dstRule.Kind()
	type squashedAttr struct {
		key  string
		attr bf.Expr
	}
	var squashedAttrs []squashedAttr
	for _, k := range srcRule.AttrKeys() {
		srcExpr := srcRule.Attr(k)
		dstExpr := dstRule.Attr(k)
		if dstExpr == nil {
			dstRule.SetAttr(k, srcExpr)
			continue
		}
		if !PreResolveAttrs[kind][k] && !PostResolveAttrs[kind][k] {
			// keep non-mergeable attributes in dst (e.g., name, visibility)
			continue
		}
		squashedExpr, err := squashExpr(srcExpr, dstExpr)
		if err != nil {
			start, end := dstExpr.Span()
			return fmt.Errorf("%s:%d.%d-%d.%d: could not squash expression", filename, start.Line, start.LineRune, end.Line, end.LineRune)
		}
		squashedAttrs = append(squashedAttrs, squashedAttr{key: k, attr: squashedExpr})
	}
	for _, a := range squashedAttrs {
		dstRule.SetAttr(a.key, a.attr)
	}
	return nil
}

func squashExpr(src, dst bf.Expr) (bf.Expr, error) {
	if shouldKeep(dst) {
		return dst, nil
	}
	if isScalar(dst) {
		// may lose src, but they should always be the same.
		return dst, nil
	}
	srcExprs, err := extractPlatformStringsExprs(src)
	if err != nil {
		return nil, err
	}
	dstExprs, err := extractPlatformStringsExprs(dst)
	if err != nil {
		return nil, err
	}
	squashedExprs, err := squashPlatformStringsExprs(srcExprs, dstExprs)
	if err != nil {
		return nil, err
	}
	return makePlatformStringsExpr(squashedExprs), nil
}

func squashPlatformStringsExprs(x, y platformStringsExprs) (platformStringsExprs, error) {
	var ps platformStringsExprs
	var err error
	if ps.generic, err = squashList(x.generic, y.generic); err != nil {
		return platformStringsExprs{}, err
	}
	if ps.os, err = squashDict(x.os, y.os); err != nil {
		return platformStringsExprs{}, err
	}
	if ps.arch, err = squashDict(x.arch, y.arch); err != nil {
		return platformStringsExprs{}, err
	}
	if ps.platform, err = squashDict(x.platform, y.platform); err != nil {
		return platformStringsExprs{}, err
	}
	return ps, nil
}

func squashList(x, y *bf.ListExpr) (*bf.ListExpr, error) {
	if x == nil {
		return y, nil
	}
	if y == nil {
		return x, nil
	}

	ls := makeListSquasher()
	for _, e := range x.List {
		s, ok := e.(*bf.StringExpr)
		if !ok {
			return nil, errors.New("could not squash non-string")
		}
		ls.add(s)
	}
	for _, e := range y.List {
		s, ok := e.(*bf.StringExpr)
		if !ok {
			return nil, errors.New("could not squash non-string")
		}
		ls.add(s)
	}
	squashed := ls.list()
	squashed.Comments.Before = append(x.Comments.Before, y.Comments.Before...)
	squashed.Comments.Suffix = append(x.Comments.Suffix, y.Comments.Suffix...)
	squashed.Comments.After = append(x.Comments.After, y.Comments.After...)
	return squashed, nil
}

func squashDict(x, y *bf.DictExpr) (*bf.DictExpr, error) {
	if x == nil {
		return y, nil
	}
	if y == nil {
		return x, nil
	}

	cases := make(map[string]*bf.KeyValueExpr)
	addCase := func(e bf.Expr) error {
		kv := e.(*bf.KeyValueExpr)
		key, ok := kv.Key.(*bf.StringExpr)
		if !ok {
			return errors.New("could not squash non-string dict key")
		}
		if _, ok := kv.Value.(*bf.ListExpr); !ok {
			return errors.New("could not squash non-list dict value")
		}
		if c, ok := cases[key.Value]; ok {
			if sq, err := squashList(kv.Value.(*bf.ListExpr), c.Value.(*bf.ListExpr)); err != nil {
				return err
			} else {
				c.Value = sq
			}
		} else {
			kvCopy := *kv
			cases[key.Value] = &kvCopy
		}
		return nil
	}

	for _, e := range x.List {
		if err := addCase(e); err != nil {
			return nil, err
		}
	}
	for _, e := range y.List {
		if err := addCase(e); err != nil {
			return nil, err
		}
	}

	keys := make([]string, 0, len(cases))
	haveDefault := false
	for k := range cases {
		if k == "//conditions:default" {
			haveDefault = true
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if haveDefault {
		keys = append(keys, "//conditions:default") // must be last
	}

	squashed := *x
	squashed.Comments.Before = append(x.Comments.Before, y.Comments.Before...)
	squashed.Comments.Suffix = append(x.Comments.Suffix, y.Comments.Suffix...)
	squashed.Comments.After = append(x.Comments.After, y.Comments.After...)
	squashed.List = make([]bf.Expr, 0, len(cases))
	for _, k := range keys {
		squashed.List = append(squashed.List, cases[k])
	}
	return &squashed, nil
}

// removeLegacyProto removes uses of the old proto rules. It deletes loads
// from go_proto_library.bzl. It deletes proto filegroups. It removes
// go_proto_library attributes which are no longer recognized. New rules
// are generated in place of the deleted rules, but attributes and comments
// are not migrated.
func removeLegacyProto(c *config.Config, f *bf.File) {
	// Don't fix if the proto mode was set to something other than the default.
	if c.ProtoMode != config.DefaultProtoMode {
		return
	}

	// Scan for definitions to delete.
	var deletedIndices []int
	var protoIndices []int
	shouldDeleteProtos := false
	for i, stmt := range f.Stmt {
		c, ok := stmt.(*bf.CallExpr)
		if !ok {
			continue
		}
		x, ok := c.X.(*bf.LiteralExpr)
		if !ok {
			continue
		}

		if x.Token == "load" && len(c.List) > 0 {
			if name, ok := c.List[0].(*bf.StringExpr); ok && name.Value == "@io_bazel_rules_go//proto:go_proto_library.bzl" {
				deletedIndices = append(deletedIndices, i)
				shouldDeleteProtos = true
			}
			continue
		}
		if x.Token == "filegroup" {
			r := bf.Rule{Call: c}
			if r.Name() == config.DefaultProtosName {
				deletedIndices = append(deletedIndices, i)
			}
			continue
		}
		if x.Token == "go_proto_library" {
			protoIndices = append(protoIndices, i)
		}
	}
	if len(deletedIndices) == 0 {
		return
	}
	if !c.ShouldFix {
		log.Printf("%s: go_proto_library.bzl is deprecated. Run 'gazelle fix' to replace old rules.", f.Path)
		return
	}

	// Rebuild the file without deleted statements. Only delete go_proto_library
	// rules if we deleted a load.
	if shouldDeleteProtos {
		deletedIndices = append(deletedIndices, protoIndices...)
		sort.Ints(deletedIndices)
	}
	f.Stmt = deleteIndices(f.Stmt, deletedIndices)
}

// flattenSrcs transforms srcs attributes structured as concatenations of
// lists and selects (generated from PlatformStrings; see
// extractPlatformStringsExprs for matching details) into a sorted,
// de-duplicated list. Comments are accumulated and de-duplicated across
// duplicate expressions.
func flattenSrcs(c *config.Config, f *bf.File) {
	for _, stmt := range f.Stmt {
		call, ok := stmt.(*bf.CallExpr)
		if !ok {
			continue
		}
		rule := bf.Rule{Call: call}
		if !isGoRule(rule.Kind()) {
			continue
		}
		oldSrcs := rule.Attr("srcs")
		if oldSrcs == nil {
			continue
		}
		flatSrcs := flattenSrcsExpr(oldSrcs)
		rule.SetAttr("srcs", flatSrcs)
	}
}

func flattenSrcsExpr(oldSrcs bf.Expr) bf.Expr {
	oldExprs, err := extractPlatformStringsExprs(oldSrcs)
	if err != nil {
		return oldSrcs
	}

	ls := makeListSquasher()
	addElem := func(e bf.Expr) bool {
		s, ok := e.(*bf.StringExpr)
		if !ok {
			return false
		}
		ls.add(s)
		return true
	}
	addList := func(e bf.Expr) bool {
		l, ok := e.(*bf.ListExpr)
		if !ok {
			return false
		}
		for _, elem := range l.List {
			if !addElem(elem) {
				return false
			}
		}
		return true
	}
	addDict := func(d *bf.DictExpr) bool {
		for _, kv := range d.List {
			if !addList(kv.(*bf.KeyValueExpr).Value) {
				return false
			}
		}
		return true
	}

	if oldExprs.generic != nil {
		if !addList(oldExprs.generic) {
			return oldSrcs
		}
	}
	for _, d := range []*bf.DictExpr{oldExprs.os, oldExprs.arch, oldExprs.platform} {
		if d == nil {
			continue
		}
		if !addDict(d) {
			return oldSrcs
		}
	}

	return ls.list()
}

// FixLoads removes loads of unused go rules and adds loads of newly used rules.
// This should be called after FixFile and MergeFile, since symbols
// may be introduced that aren't loaded.
func FixLoads(f *bf.File) {
	// Make a list of load statements in the file. Keep track of loads of known
	// files, since these may be changed. Keep track of known symbols loaded from
	// unknown files; we will not add loads for these.
	type loadInfo struct {
		index      int
		file       string
		old, fixed *bf.CallExpr
	}
	var loads []loadInfo
	otherLoadedKinds := make(map[string]bool)
	for i, stmt := range f.Stmt {
		c, ok := stmt.(*bf.CallExpr)
		if !ok {
			continue
		}
		x, ok := c.X.(*bf.LiteralExpr)
		if !ok || x.Token != "load" {
			continue
		}

		if len(c.List) == 0 {
			continue
		}
		label, ok := c.List[0].(*bf.StringExpr)
		if !ok {
			continue
		}

		if knownFiles[label.Value] {
			loads = append(loads, loadInfo{index: i, file: label.Value, old: c})
			continue
		}
		for _, arg := range c.List[1:] {
			switch sym := arg.(type) {
			case *bf.StringExpr:
				otherLoadedKinds[sym.Value] = true
			case *bf.BinaryExpr:
				if sym.Op != "=" {
					continue
				}
				if x, ok := sym.X.(*bf.LiteralExpr); ok {
					otherLoadedKinds[x.Token] = true
				}
			}
		}
	}

	// Make a map of all the symbols from known files used in this file.
	usedKinds := make(map[string]map[string]bool)
	for _, stmt := range f.Stmt {
		c, ok := stmt.(*bf.CallExpr)
		if !ok {
			continue
		}
		x, ok := c.X.(*bf.LiteralExpr)
		if !ok {
			continue
		}

		kind := x.Token
		if file, ok := knownKinds[kind]; ok && !otherLoadedKinds[kind] {
			if usedKinds[file] == nil {
				usedKinds[file] = make(map[string]bool)
			}
			usedKinds[file][kind] = true
		}
	}

	// Fix the load statements. The order is important, so we iterate over
	// knownLoads instead of knownFiles.
	changed := false
	type newLoad struct {
		index int
		load  *bf.CallExpr
	}
	var newLoads []newLoad
	for _, l := range knownLoads {
		file := l.file
		first := true
		for i, _ := range loads {
			li := &loads[i]
			if li.file != file {
				continue
			}
			if first {
				li.fixed = fixLoad(li.old, file, usedKinds[file])
				first = false
			} else {
				li.fixed = fixLoad(li.old, file, nil)
			}
			changed = changed || li.fixed != li.old
		}
		if first {
			load := fixLoad(nil, file, usedKinds[file])
			if load != nil {
				index := newLoadIndex(f.Stmt, l.after)
				newLoads = append(newLoads, newLoad{index, load})
				changed = true
			}
		}
	}
	if !changed {
		return
	}
	sort.Slice(newLoads, func(i, j int) bool {
		return newLoads[i].index < newLoads[j].index
	})

	// Rebuild the file. Insert new loads at appropriate indices, replace fixed
	// loads, and drop deleted loads.
	oldStmt := f.Stmt
	f.Stmt = make([]bf.Expr, 0, len(oldStmt)+len(newLoads))
	newLoadIndex := 0
	loadIndex := 0
	for i, stmt := range oldStmt {
		for newLoadIndex < len(newLoads) && i == newLoads[newLoadIndex].index {
			f.Stmt = append(f.Stmt, newLoads[newLoadIndex].load)
			newLoadIndex++
		}
		if loadIndex < len(loads) && i == loads[loadIndex].index {
			if loads[loadIndex].fixed != nil {
				f.Stmt = append(f.Stmt, loads[loadIndex].fixed)
			}
			loadIndex++
			continue
		}
		f.Stmt = append(f.Stmt, stmt)
	}
}

// knownLoads is a list of files Gazelle will generate loads from and
// the symbols it knows about. All symbols Gazelle ever generated
// loads for are present, including symbols it no longer uses (e.g.,
// cgo_library). Manually loaded symbols (e.g., go_embed_data) are not
// included.
//
// Some symbols have a list of function calls that they should be loaded
// after. This is important for WORKSPACE, where function calls may
// introduce new repository names.
//
// The order of the files here will match the order of generated load
// statements. The symbols should be sorted lexicographically. If a
// symbol appears in more than one file (e.g., because it was moved),
// it will be loaded from the last file in this list.
var knownLoads = []struct {
	file  string
	kinds []string
	after []string
}{
	{
		file: "@io_bazel_rules_go//go:def.bzl",
		kinds: []string{
			"cgo_library",
			"go_binary",
			"go_library",
			"go_prefix",
			"go_repository",
			"go_test",
		},
	}, {
		file: "@io_bazel_rules_go//proto:def.bzl",
		kinds: []string{
			"go_grpc_library",
			"go_proto_library",
		},
	}, {
		file: "@bazel_gazelle//:deps.bzl",
		kinds: []string{
			"go_repository",
		},
		after: []string{
			"go_rules_dependencies",
			"go_register_toolchains",
			"gazelle_dependencies",
		},
	},
}

// knownFiles is the set of labels for files that Gazelle loads symbols from.
var knownFiles map[string]bool

// knownKinds is a map from symbols to labels of the files they are loaded
// from.
var knownKinds map[string]string

func init() {
	knownFiles = make(map[string]bool)
	knownKinds = make(map[string]string)
	for _, l := range knownLoads {
		knownFiles[l.file] = true
		for _, k := range l.kinds {
			knownKinds[k] = l.file
		}
	}
}

// fixLoad updates a load statement. load must be a load statement for
// the Go rules or nil. If nil, a new statement may be created. Symbols in
// kinds are added if they are not already present, symbols in knownKinds
// are removed if they are not in kinds, and other symbols and arguments
// are preserved. nil is returned if the statement should be deleted because
// it is empty.
func fixLoad(load *bf.CallExpr, file string, kinds map[string]bool) *bf.CallExpr {
	var fixed bf.CallExpr
	if load == nil {
		fixed = bf.CallExpr{
			X: &bf.LiteralExpr{Token: "load"},
			List: []bf.Expr{
				&bf.StringExpr{Value: file},
			},
			ForceCompact: true,
		}
	} else {
		fixed = *load
	}

	var symbols []*bf.StringExpr
	var otherArgs []bf.Expr
	loadedKinds := make(map[string]bool)
	var added, removed int
	for _, arg := range fixed.List[1:] {
		if s, ok := arg.(*bf.StringExpr); ok {
			if knownKinds[s.Value] == "" || kinds != nil && kinds[s.Value] {
				symbols = append(symbols, s)
				loadedKinds[s.Value] = true
			} else {
				removed++
			}
		} else {
			otherArgs = append(otherArgs, arg)
		}
	}
	if kinds != nil {
		for kind, _ := range kinds {
			if _, ok := loadedKinds[kind]; !ok {
				symbols = append(symbols, &bf.StringExpr{Value: kind})
				added++
			}
		}
	}
	if added == 0 && removed == 0 {
		if load != nil && len(load.List) == 1 {
			// Special case: delete existing empty load.
			return nil
		}
		return load
	}

	sort.Stable(byString(symbols))
	fixed.List = fixed.List[:1]
	for _, sym := range symbols {
		fixed.List = append(fixed.List, sym)
	}
	fixed.List = append(fixed.List, otherArgs...)
	if len(fixed.List) == 1 {
		return nil
	}
	return &fixed
}

// newLoadIndex returns the index in stmts where a new load statement should
// be inserted. after is a list of function names that the load should not
// be inserted before.
func newLoadIndex(stmts []bf.Expr, after []string) int {
	if len(after) == 0 {
		return 0
	}
	index := 0
	for i, stmt := range stmts {
		call, ok := stmt.(*bf.CallExpr)
		if !ok {
			continue
		}
		x, ok := call.X.(*bf.LiteralExpr)
		if !ok {
			continue
		}
		for _, a := range after {
			if x.Token == a {
				index = i + 1
			}
		}
	}
	return index
}

// FixWorkspace updates rules in the WORKSPACE file f that were used with an
// older version of rules_go or gazelle.
func FixWorkspace(f *bf.File) {
	removeLegacyGoRepository(f)
}

// CheckGazelleLoaded searches the given WORKSPACE file for a repository named
// "bazel_gazelle". If no such repository is found *and* the repo is not
// declared with a directive *and* at least one load statement mentions
// the repository, a descriptive error will be returned.
//
// This should be called after modifications have been made to WORKSPACE
// (i.e., after FixLoads) before writing it to disk.
func CheckGazelleLoaded(f *bf.File) error {
	needGazelle := false
	for _, stmt := range f.Stmt {
		call, ok := stmt.(*bf.CallExpr)
		if !ok {
			continue
		}
		x, ok := call.X.(*bf.LiteralExpr)
		if !ok {
			continue
		}
		if x.Token == "load" {
			if len(call.List) == 0 {
				continue
			}
			if s, ok := call.List[0].(*bf.StringExpr); ok && strings.HasPrefix(s.Value, "@bazel_gazelle//") {
				needGazelle = true
			}
			continue
		}
		rule := bf.Rule{Call: call}
		if rule.Name() == "bazel_gazelle" {
			return nil
		}
	}
	if !needGazelle {
		return nil
	}
	for _, d := range config.ParseDirectives(f) {
		if d.Key != "repo" {
			continue
		}
		if fs := strings.Fields(d.Value); len(fs) > 0 && fs[0] == "bazel_gazelle" {
			return nil
		}
	}
	return fmt.Errorf(`%s: error: bazel_gazelle is not declared in WORKSPACE.
Without this repository, Gazelle cannot safely modify the WORKSPACE file.
See the instructions at https://github.com/bazelbuild/bazel-gazelle.
If the bazel_gazelle is declared inside a macro, you can suppress this error
by adding a comment like this to WORKSPACE:
    # gazelle:repo bazel_gazelle
`, f.Path)
}

// removeLegacyGoRepository removes loads of go_repository from
// @io_bazel_rules_go. FixLoads should be called after this; it will load from
// @bazel_gazelle.
func removeLegacyGoRepository(f *bf.File) {
	var deletedStmtIndices []int
	for stmtIndex, stmt := range f.Stmt {
		call, ok := stmt.(*bf.CallExpr)
		if !ok || len(call.List) < 1 {
			continue
		}
		if x, ok := call.X.(*bf.LiteralExpr); !ok || x.Token != "load" {
			continue
		}
		if path, ok := call.List[0].(*bf.StringExpr); !ok || path.Value != "@io_bazel_rules_go//go:def.bzl" {
			continue
		}
		var deletedArgIndices []int
		for argIndex, arg := range call.List {
			str, ok := arg.(*bf.StringExpr)
			if !ok {
				continue
			}
			if str.Value == "go_repository" {
				deletedArgIndices = append(deletedArgIndices, argIndex)
			}
		}
		if len(call.List)-len(deletedArgIndices) == 1 {
			deletedStmtIndices = append(deletedStmtIndices, stmtIndex)
		} else {
			call.List = deleteIndices(call.List, deletedArgIndices)
		}
	}
	f.Stmt = deleteIndices(f.Stmt, deletedStmtIndices)
}

// listSquasher builds a sorted, deduplicated list of string expressions. If
// a string expression is added multiple times, comments are consolidated.
// The original expressions are not modified.
type listSquasher struct {
	unique       map[string]*bf.StringExpr
	seenComments map[elemComment]bool
}

type elemComment struct {
	elem, com string
}

func makeListSquasher() listSquasher {
	return listSquasher{
		unique:       make(map[string]*bf.StringExpr),
		seenComments: make(map[elemComment]bool),
	}
}

func (ls *listSquasher) add(s *bf.StringExpr) {
	sCopy, ok := ls.unique[s.Value]
	if !ok {
		// Make a copy of s. We may modify it when we consolidate comments from
		// duplicate strings. We don't want to modify the original in case this
		// function fails (due to a later failed pattern match).
		sCopy = new(bf.StringExpr)
		*sCopy = *s
		sCopy.Comments.Before = make([]bf.Comment, 0, len(s.Comments.Before))
		sCopy.Comments.Suffix = make([]bf.Comment, 0, len(s.Comments.Suffix))
		ls.unique[s.Value] = sCopy
	}
	for _, c := range s.Comment().Before {
		if key := (elemComment{s.Value, c.Token}); !ls.seenComments[key] {
			sCopy.Comments.Before = append(sCopy.Comments.Before, c)
			ls.seenComments[key] = true
		}
	}
	for _, c := range s.Comment().Suffix {
		if key := (elemComment{s.Value, c.Token}); !ls.seenComments[key] {
			sCopy.Comments.Suffix = append(sCopy.Comments.Suffix, c)
			ls.seenComments[key] = true
		}
	}
}

func (ls *listSquasher) list() *bf.ListExpr {
	sortedExprs := make([]bf.Expr, 0, len(ls.unique))
	for _, e := range ls.unique {
		sortedExprs = append(sortedExprs, e)
	}
	sort.Slice(sortedExprs, func(i, j int) bool {
		return sortedExprs[i].(*bf.StringExpr).Value < sortedExprs[j].(*bf.StringExpr).Value
	})
	return &bf.ListExpr{List: sortedExprs}
}

type byString []*bf.StringExpr

func (s byString) Len() int {
	return len(s)
}

func (s byString) Less(i, j int) bool {
	return s[i].Value < s[j].Value
}

func (s byString) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func isGoRule(kind string) bool {
	return kind == "go_library" ||
		kind == "go_binary" ||
		kind == "go_test" ||
		kind == "go_proto_library" ||
		kind == "go_grpc_library"
}
