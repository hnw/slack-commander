package cmd

import (
	"reflect"
	"testing"
)

func TestMatcher(t *testing.T) {
	cfgs := []*CommandConfig{
		{
			Definition: &Definition{
				Keyword: `ping 8.8.8.8`,
				Command: `ping -c4 8.8.8.8`,
			},
		},
		{
			Definition: &Definition{
				Keyword: `ping *`,
				Command: `ping * -c4`,
			},
		},
		{
			Definition: &Definition{
				Keyword: `ping *`,
				Command: `/bin/sh -c "ping *"`,
			},
		},
		{
			Definition: &Definition{
				Keyword: `echo *`,
				Command: `/bin/echo *`,
			},
		},
		{
			Definition: &Definition{
				Keyword: `echo *`,
				Command: `/bin/echo "*"`,
			},
		},
		{
			Definition: &Definition{
				Keyword: `echo *`,
				Command: `/bin/echo '*'`,
			},
		},
		{
			Definition: &Definition{
				Keyword: `foo * bar`,
				Command: `*`,
			},
		},
		{
			Definition: &Definition{
				Keyword: `openurl *`,
				Command: `pwopen --no-sandbox *`,
			},
		},
		{
			Definition: &Definition{
				Keyword: `openurl *`,
				Command: `pwopen --no-sandbox *`,
			},
		},
		{
			Definition: &Definition{
				Keyword: `openurl`,
				Command: `pwopen --no-sandbox`,
			},
		},
		{
			Definition: &Definition{
				Keyword: `deploy * bar`,
				Command: `deploy * bar`,
			},
		},
	}
	args := [][]string{
		{`ping`, `8.8.8.8`},
		{`ping`, `-i2`, `8.8.8.8`},
		{`ping`, `-i2`, `8.8.8.8`},
		{`echo`, `foo bar`, `baz`},
		{`echo`, `foo bar`, `baz`},
		{`echo`, `foo bar`, `baz`},
		{`foo`, `baz`, `qux`, `quux`, `bar`},
		{`openurl`},
		{`openurl`, `http://example.com`},
		{`openurl`, `http://example.com`},
		{`deploy`},
	}
	expects := [][]string{
		{`ping`, `-c4`, `8.8.8.8`},
		{`ping`, `-i2`, `8.8.8.8`, `-c4`},
		{`/bin/sh`, `-c`, `ping -i2 8.8.8.8`},
		{`/bin/echo`, `foo bar`, `baz`},
		{`/bin/echo`, `foo bar baz`},
		{`/bin/echo`, `foo\ bar baz`},
		{`baz`, `qux`, `quux`},
		{`pwopen`, `--no-sandbox`},
		{`pwopen`, `--no-sandbox`, `http://example.com`},
		nil,
		nil,
	}

	for i, cfg := range cfgs {
		m := newMatcher(cfg)
		result := m.build(args[i])

		if !reflect.DeepEqual(result, expects[i]) {
			t.Errorf(`Unexpected result for test#%v: expected=%v, actual=%v`, i+1, expects[i], result)
		}
	}
}
