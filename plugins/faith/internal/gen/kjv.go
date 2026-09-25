package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Nomadcxx/sysc-plugins/plugins/faith"
)

// kjvNames are the source's book names, in the canon's order.
var kjvNames = []string{
	"Genesis", "Exodus", "Leviticus", "Numbers", "Deuteronomy", "Joshua", "Judges", "Ruth",
	"I Samuel", "II Samuel", "I Kings", "II Kings", "I Chronicles", "II Chronicles", "Ezra",
	"Nehemiah", "Esther", "Job", "Psalms", "Proverbs", "Ecclesiastes", "Song of Solomon",
	"Isaiah", "Jeremiah", "Lamentations", "Ezekiel", "Daniel", "Hosea", "Joel", "Amos",
	"Obadiah", "Jonah", "Micah", "Nahum", "Habakkuk", "Zephaniah", "Haggai", "Zechariah",
	"Malachi", "Matthew", "Mark", "Luke", "John", "Acts", "Romans", "I Corinthians",
	"II Corinthians", "Galatians", "Ephesians", "Philippians", "Colossians", "I Thessalonians",
	"II Thessalonians", "I Timothy", "II Timothy", "Titus", "Philemon", "Hebrews", "James",
	"I Peter", "II Peter", "I John", "II John", "III John", "Jude", "Revelation of John",
}

// ParseKJV reads scrollmapper's KJV.json into verses per book index.
func ParseKJV(r io.Reader) ([][]Verse, error) {
	var src struct {
		Books []struct {
			Name     string `json:"name"`
			Chapters []struct {
				Chapter int `json:"chapter"`
				Verses  []struct {
					Verse int    `json:"verse"`
					Text  string `json:"text"`
				} `json:"verses"`
			} `json:"chapters"`
		} `json:"books"`
	}
	if err := json.NewDecoder(r).Decode(&src); err != nil {
		return nil, fmt.Errorf("kjv: %w", err)
	}
	if len(src.Books) != len(faith.Books) {
		return nil, fmt.Errorf("kjv: %d books, want %d", len(src.Books), len(faith.Books))
	}
	out := make([][]Verse, len(src.Books))
	for i, b := range src.Books {
		if b.Name != kjvNames[i] {
			return nil, fmt.Errorf("kjv: book %d is %q, want %q", i, b.Name, kjvNames[i])
		}
		for _, c := range b.Chapters {
			for _, v := range c.Verses {
				out[i] = append(out[i], Verse{Chapter: c.Chapter, Verse: v.Verse, Text: clean(v.Text)})
			}
		}
	}
	return out, nil
}
