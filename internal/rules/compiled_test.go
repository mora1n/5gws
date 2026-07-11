package rules

import (
	"fmt"
	"testing"
)

func TestCompiledPreservesFirstMatch(t *testing.T) {
	norm := Normalized{Rules: []Rule{
		{Name: "early-suffix", Exit: "a", DomainSuffix: []string{"example.com"}},
		{Name: "later-exact", Exit: "b", Domain: []string{"api.example.com"}},
		{Name: "keyword", Exit: "c", DomainKeyword: []string{"special"}},
	}}
	compiled, err := Compile(norm)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"api.example.com":      "early-suffix",
		"www.example.com":      "early-suffix",
		"special.test.invalid": "keyword",
	}
	for host, want := range cases {
		got, ok := compiled.MatchGatewayDomain(host)
		if !ok || got.Name != want {
			t.Fatalf("match %s = %q, %t; want %q", host, got.Name, ok, want)
		}
	}
}

func TestCompiledRegexError(t *testing.T) {
	_, err := Compile(Normalized{Rules: []Rule{{Name: "bad", Exit: "direct", DomainRegex: []string{"["}}}})
	if err == nil {
		t.Fatal("expected regex compilation error")
	}
}

func BenchmarkCompiledMatch10K(b *testing.B) {
	rules := make([]Rule, 10_000)
	for i := range rules {
		rules[i] = Rule{Name: fmt.Sprint(i), Exit: "direct", DomainSuffix: []string{fmt.Sprintf("d%d.example", i)}}
	}
	compiled, err := Compile(Normalized{Rules: rules})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok := compiled.MatchGatewayDomain("www.d9999.example"); !ok {
			b.Fatal("no match")
		}
	}
}
