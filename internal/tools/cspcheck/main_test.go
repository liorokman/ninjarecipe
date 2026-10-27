package main

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	good := `<script src="{{asset "js/app.js"}}"></script><script type="application/json" id="x">{{.V}}</script><a href="{{url "home"}}">x</a><form action="{{url "lang"}}"></form>`
	if v := check(good); len(v) != 0 {
		t.Fatalf("good template flagged: %v", v)
	}
	bads := map[string]string{
		"inline <script> code":      `<script>alert(1)</script>`,
		"on*= attribute":            `<button onclick="x()">`,
		"style= attribute":          `<div style="color:red">`,
		"external script/style URL": `<script src="https://cdn.example.com/a.js"></script>`,
		"hard-coded path":           `<a href="/recipes">x</a>`,
	}
	for want, src := range bads {
		found := false
		for _, v := range check(src) {
			if strings.Contains(v, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q not detected in %s", want, src)
		}
	}
}
