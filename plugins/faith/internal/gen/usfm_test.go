package main

import (
	"strings"
	"testing"
)

const bsbFixture = `\id JHN - Berean Study Bible
\h John
\mt1 John
\c 3
\s1 Jesus and Nicodemus
\r (John 7:45–53)
\p
\v 16 For God so loved the world that He gave His one and only \f + \fr 3:16 \ft Or only begotten or unique; also in verse 18\f* Son, that everyone who believes in Him shall not perish but have eternal life.
\v 17 For God did not send His Son into the world to condemn the world, but to save the world through Him.
`

const psalmFixture = `\c 23
\d A Psalm of David.
\b
\q1
\v 1 The LORD is my shepherd;
\q2 I shall not want.
\q1
\v 2 He makes me lie down in green pastures;
\q2 He leads me beside quiet waters.
`

const webFixture = `\c 1
\p
\v 1  \w In|strong="G1722"\w* \w the|strong="G3588"\w* \w beginning|strong="G0746"\w* \w was|strong="G1510"\w* \w the|strong="G3588"\w* \w Word|strong="G3056"\w*, \wj \+w and|strong="G2532"\+w* \+w so|strong="G2532"\+w*\wj* \x + \xo 1:1 \xt Gen 1:1\x* on.
`

func TestParseUSFMKeepsVerseTextOnly(t *testing.T) {
	got, err := ParseUSFM(strings.NewReader(bsbFixture))
	if err != nil {
		t.Fatal(err)
	}
	want := []Verse{
		{3, 16, "For God so loved the world that He gave His one and only Son, that everyone who believes in Him shall not perish but have eternal life."},
		{3, 17, "For God did not send His Son into the world to condemn the world, but to save the world through Him."},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d verses: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("verse %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func TestParseUSFMJoinsPoetryAndDropsSuperscriptions(t *testing.T) {
	got, err := ParseUSFM(strings.NewReader(psalmFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Text != "The LORD is my shepherd; I shall not want." ||
		got[1].Text != "He makes me lie down in green pastures; He leads me beside quiet waters." {
		t.Fatalf("got %+v", got)
	}
}

func TestParseUSFMStripsStrongsAndNestedWords(t *testing.T) {
	got, err := ParseUSFM(strings.NewReader(webFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "In the beginning was the Word, and so on." {
		t.Fatalf("got %+v", got)
	}
}

// Ephesians 3:14 in the BSB source runs its ellipsis into the verse number.
func TestParseUSFMReadsAVerseNumberWithNoSpaceAfterIt(t *testing.T) {
	got, err := ParseUSFM(strings.NewReader("\\c 3\n\\v 14... for this reason I bow my knees\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Verse != 14 || got[0].Text != "... for this reason I bow my knees" {
		t.Fatalf("got %+v", got)
	}
}

// Habakkuk 3:19 in the BSB source closes an italic run on a line of its own.
func TestParseUSFMTreatsALoneClosingMarkerAsText(t *testing.T) {
	src := "\\c 3\n\\v 19 He makes me walk upon the heights!\n\\pc\n\\it For the choirmaster.\n\\it*\n"
	got, err := ParseUSFM(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "He makes me walk upon the heights! For the choirmaster." {
		t.Fatalf("got %+v", got)
	}
}

func TestParseUSFMRejectsAVerseBeforeAChapter(t *testing.T) {
	if _, err := ParseUSFM(strings.NewReader(`\v 1 text`)); err == nil {
		t.Fatal("accepted a verse before a chapter")
	}
}
