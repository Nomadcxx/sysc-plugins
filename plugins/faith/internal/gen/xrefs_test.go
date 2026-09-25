package main

import (
	"reflect"
	"strings"
	"testing"
)

const xrefFixture = "From Verse\tTo Verse\tVotes\t#www.openbible.info CC-BY 2024-11-04\n" +
	"Gen.1.1\tProv.8.22-Prov.8.30\t59\n" +
	"Gen.1.1\tZech.12.1\t49\n" +
	"Gen.1.1\tActs.14.15\t62\n" +
	"Gen.1.1\t1Chr.16.26\t43\n" +
	"Gen.1.1\tJohn.1.1-John.2.3\t70\n" +
	"Gen.1.1\tHeb.11.3\t49\n" +
	"Gen.1.1\tRev.4.11\t12\n" +
	"Gen.1.1\tIsa.44.24\t0\n" +
	"Gen.1.1\tPs.33.6\t-3\n" +
	"John.3.16\tRom.5.8\t300\n"

func TestParseXrefsKeepsTheBestFive(t *testing.T) {
	got, err := ParseXrefs(strings.NewReader(xrefFixture))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		// John 1:1–2:3 crosses a chapter, so it keeps its first verse; the
		// 49-vote tie orders canonically; zero and negative votes are gone.
		"GEN 1:1":  {"JHN 1:1", "ACT 14:15", "PRO 8:22-30", "ZEC 12:1", "HEB 11:3"},
		"JHN 3:16": {"ROM 5:8"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestParseXrefsRejectsUnknownBooks(t *testing.T) {
	if _, err := ParseXrefs(strings.NewReader("Gen.1.1\tTob.1.1\t5\n")); err == nil {
		t.Fatal("accepted Tobit")
	}
}
