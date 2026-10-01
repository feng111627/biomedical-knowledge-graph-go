package kg

import "sort"

// sortStrings 是对 sort.Strings 的内部封装，便于稳定排序。
func sortStrings(s []string) { sort.Strings(s) }
