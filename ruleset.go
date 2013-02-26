// Mkfiles are parsed into ruleSets, which as the name suggests, are sets of
// rules with accompanying recipes, as well as assigned variables which are
// expanding when evaluating rules and recipes.

package main

import (
	"unicode/utf8"
)

type attribSet struct {
	delFailed       bool // delete targets when the recipe fails
	nonstop         bool // don't stop if the recipe fails
	forcedTimestamp bool // update timestamp whether the recipe does or not
	nonvirtual      bool // a meta-rule that will only match files
	quiet           bool // don't print the recipe
	regex           bool // regular expression meta-rule
	update          bool // treat the targets as if they were updated
	virtual         bool // rule is virtual (does not match files)
}

// Error parsing an attribute
type attribError struct {
	found rune
}

type rule struct {
	targets    []string  // non-empty array of targets
	attributes attribSet // rule attributes
	prereqs    []string  // possibly empty prerequesites
	shell      []string  // command used to execute the recipe
	recipe     string    // recipe source
	command    []string  // command attribute
}

// Read attributes for an array of strings, updating the rule.
func (r *rule) parseAttribs(inputs []string) *attribError {
	for i := 0; i < len(inputs); i++ {
		input := inputs[i]
		pos := 0
		for pos < len(input) {
			c, w := utf8.DecodeRuneInString(input[pos:])
			switch c {
			case 'D':
				r.attributes.delFailed = true
			case 'E':
				r.attributes.nonstop = true
			case 'N':
				r.attributes.forcedTimestamp = true
			case 'n':
				r.attributes.nonvirtual = true
			case 'Q':
				r.attributes.quiet = true
			case 'R':
				r.attributes.regex = true
			case 'U':
				r.attributes.update = true
			case 'V':
				r.attributes.virtual = true
			case 'P':
				if pos+w < len(input) {
					r.command = append(r.command, input[pos+w:])
				}
				r.command = append(r.command, inputs[i+1:]...)
				return nil

			case 'S':
				if pos+w < len(input) {
					r.shell = append(r.shell, input[pos+w:])
				}
				r.shell = append(r.shell, inputs[i+1:]...)
				return nil

			default:
				return &attribError{c}
			}

			pos += w
		}
	}

	return nil
}

type ruleSet struct {
	vars  map[string][]string
	rules []rule
}

// Add a rule to the rule set.
func (rs *ruleSet) push(r rule) {
	rs.rules = append(rs.rules, r)
}

// Expand variables found in a string.
func (rs *ruleSet) expand(t token) string {
	// TODO: implement this
	return t.val
}

func isValidVarName(v string) bool {
	for i := 0; i < len(v); {
		c, w := utf8.DecodeRuneInString(v[i:])
		if i == 0 && !(isalpha(c) || c == '_') {
			return false
		} else if !isalnum(c) || c == '_' {
			return false
		}
		i += w
	}
	return true
}

func isalpha(c rune) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func isalnum(c rune) bool {
	return isalpha(c) || ('0' <= c && c <= '9')
}

// Parse and execute assignment operation.
func (rs *ruleSet) executeAssignment(ts []token) {
	assignee := ts[0].val
	if !isValidVarName(assignee) {
		// TODO: complain
	}

	// expanded variables
	vals := make([]string, len(ts)-1)
	for i := 0; i < len(vals); i++ {
		vals[i] = rs.expand(ts[i+1])
	}

	rs.vars[assignee] = vals
}