package tools

import (
	"regexp"
)

// compileGlob — скомпилировать glob в регулярное выражение.
func compileGlob(pat string) (*regexp.Regexp, error) {
	return regexp.Compile(GlobToRegexp(pat))
}

// compileRegexp — скомпилировать регулярное выражение пользователя.
func compileRegexp(pat string) (*regexp.Regexp, error) {
	return regexp.Compile(pat)
}
