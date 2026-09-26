package faith

import "fmt"

// Traditions a prayer can belong to. Ecumenical prayers are in every pool;
// the others join it when the user picks that tradition.
const (
	Ecumenical = "ecumenical"
	Catholic   = "catholic"
	Orthodox   = "orthodox"
)

// ValidTradition reports whether t is a tradition setting value.
func ValidTradition(t string) bool {
	return t == Ecumenical || t == Catholic || t == Orthodox
}

// Prayer is one public-domain prayer. A Scripture prayer names a Ref instead
// of carrying Text, and reads its words from the user's translation.
type Prayer struct {
	ID        string
	Title     string
	Tradition string
	Text      string
	Ref       string
	Source    string
}

// Body is the prayer's words and the source line a notification shows.
func (p Prayer) Body(b *Bible) (text, source string, err error) {
	if p.Ref == "" {
		return p.Text, p.Source, nil
	}
	r, err := ParseRef(p.Ref)
	if err != nil {
		return "", "", err
	}
	text, ok := b.Text(r)
	if !ok {
		return "", "", fmt.Errorf("faith: %s has no %s", b.ID, p.Ref)
	}
	return text, fmt.Sprintf("%s (%s)", r.String(), b.ID), nil
}

// Prayers is the corpus. Every text is public domain: Scripture, the historic
// creeds and hymns, the 1928 and 1979 Books of Common Prayer (the US
// editions, both public domain), the 1891 Baltimore Catechism, Thomas Ken
// (1674), and C. F. Alexander (1889). The Orthodox and several Catholic
// prayers are given in their traditional English wording.
var Prayers = []Prayer{
	{ID: "lords-prayer", Title: "The Lord's Prayer", Tradition: Ecumenical, Source: "Book of Common Prayer, 1928",
		Text: "Our Father, who art in heaven, Hallowed be thy Name. Thy kingdom come. Thy will be done, On earth as it is in heaven. Give us this day our daily bread. And forgive us our trespasses, As we forgive those who trespass against us. And lead us not into temptation, But deliver us from evil: For thine is the kingdom, and the power, and the glory, for ever and ever. Amen."},
	{ID: "apostles-creed", Title: "The Apostles' Creed", Tradition: Ecumenical, Source: "Book of Common Prayer, 1928",
		Text: "I believe in God the Father Almighty, Maker of heaven and earth: And in Jesus Christ his only Son our Lord: Who was conceived by the Holy Ghost, Born of the Virgin Mary: Suffered under Pontius Pilate, Was crucified, dead, and buried: He descended into hell; The third day he rose again from the dead: He ascended into heaven, And sitteth on the right hand of God the Father Almighty: From thence he shall come to judge the quick and the dead. I believe in the Holy Ghost: The holy Catholic Church; The Communion of Saints: The Forgiveness of sins: The Resurrection of the body: And the Life everlasting. Amen."},
	{ID: "nicene-creed", Title: "The Nicene Creed", Tradition: Ecumenical, Source: "Book of Common Prayer, 1928",
		Text: "I believe in one God the Father Almighty, Maker of heaven and earth, And of all things visible and invisible: And in one Lord Jesus Christ, the only-begotten Son of God; Begotten of his Father before all worlds, God of God, Light of Light, Very God of very God; Begotten, not made; Being of one substance with the Father; By whom all things were made: Who for us men and for our salvation came down from heaven, And was incarnate by the Holy Ghost of the Virgin Mary, And was made man: And was crucified also for us under Pontius Pilate; He suffered and was buried: And the third day he rose again according to the Scriptures: And ascended into heaven, And sitteth on the right hand of the Father: And he shall come again, with glory, to judge both the quick and the dead; Whose kingdom shall have no end. And I believe in the Holy Ghost, The Lord, and Giver of Life, Who proceedeth from the Father and the Son; Who with the Father and the Son together is worshipped and glorified; Who spake by the Prophets: And I believe one Catholic and Apostolic Church: I acknowledge one Baptism for the remission of sins: And I look for the Resurrection of the dead: And the Life of the world to come. Amen."},
	{ID: "gloria-patri", Title: "Glory Be", Tradition: Ecumenical, Source: "Book of Common Prayer, 1928",
		Text: "Glory be to the Father, and to the Son, and to the Holy Ghost; As it was in the beginning, is now, and ever shall be, world without end. Amen."},
	{ID: "doxology", Title: "The Doxology", Tradition: Ecumenical, Source: "Thomas Ken, 1674",
		Text: "Praise God, from whom all blessings flow; Praise him, all creatures here below; Praise him above, ye heavenly host; Praise Father, Son, and Holy Ghost. Amen."},
	{ID: "aaronic-blessing", Title: "The Aaronic Blessing", Tradition: Ecumenical, Ref: "NUM 6:24-26"},
	{ID: "the-grace", Title: "The Grace", Tradition: Ecumenical, Source: "Book of Common Prayer, 1928 (2 Corinthians 13:14)",
		Text: "The grace of our Lord Jesus Christ, and the love of God, and the fellowship of the Holy Ghost, be with us all evermore. Amen."},
	{ID: "jesus-prayer", Title: "The Jesus Prayer", Tradition: Ecumenical, Source: "Traditional",
		Text: "Lord Jesus Christ, Son of God, have mercy on me, a sinner."},
	{ID: "st-francis", Title: "A Prayer Attributed to St. Francis", Tradition: Ecumenical, Source: "Book of Common Prayer, 1979",
		Text: "Lord, make us instruments of your peace. Where there is hatred, let us sow love; where there is injury, pardon; where there is discord, union; where there is doubt, faith; where there is despair, hope; where there is darkness, light; where there is sadness, joy. Grant that we may not so much seek to be consoled as to console; to be understood as to understand; to be loved as to love. For it is in giving that we receive; it is in pardoning that we are pardoned; and it is in dying that we are born to eternal life. Amen."},
	{ID: "st-patrick", Title: "From St. Patrick's Breastplate", Tradition: Ecumenical, Source: "St. Patrick, tr. C. F. Alexander, 1889",
		Text: "Christ be with me, Christ within me, Christ behind me, Christ before me, Christ beside me, Christ to win me, Christ to comfort and restore me. Christ beneath me, Christ above me, Christ in quiet, Christ in danger, Christ in hearts of all that love me, Christ in mouth of friend and stranger."},
	{ID: "collect-purity", Title: "The Collect for Purity", Tradition: Ecumenical, Source: "Book of Common Prayer, 1928",
		Text: "Almighty God, unto whom all hearts are open, all desires known, and from whom no secrets are hid; Cleanse the thoughts of our hearts by the inspiration of thy Holy Spirit, that we may perfectly love thee, and worthily magnify thy holy Name; through Christ our Lord. Amen."},
	{ID: "collect-grace", Title: "A Collect for Grace", Tradition: Ecumenical, Source: "Book of Common Prayer, 1928",
		Text: "O Lord, our heavenly Father, Almighty and everlasting God, who hast safely brought us to the beginning of this day; Defend us in the same with thy mighty power; and grant that this day we fall into no sin, neither run into any kind of danger; but that all our doings, being ordered by thy governance, may be righteous in thy sight; through Jesus Christ our Lord. Amen."},
	{ID: "collect-peace", Title: "A Collect for Peace", Tradition: Ecumenical, Source: "Book of Common Prayer, 1928",
		Text: "O God, who art the author of peace and lover of concord, in knowledge of whom standeth our eternal life, whose service is perfect freedom; Defend us thy humble servants in all assaults of our enemies; that we, surely trusting in thy defence, may not fear the power of any adversaries; through the might of Jesus Christ our Lord. Amen."},
	{ID: "collect-aid", Title: "A Collect for Aid against Perils", Tradition: Ecumenical, Source: "Book of Common Prayer, 1928",
		Text: "Lighten our darkness, we beseech thee, O Lord; and by thy great mercy defend us from all perils and dangers of this night; for the love of thy only Son, our Saviour, Jesus Christ. Amen."},
	{ID: "collect-guidance", Title: "For Guidance", Tradition: Ecumenical, Source: "Book of Common Prayer, 1928",
		Text: "Direct us, O Lord, in all our doings with thy most gracious favour, and further us with thy continual help; that in all our works begun, continued, and ended in thee, we may glorify thy holy Name, and finally, by thy mercy, obtain everlasting life; through Jesus Christ our Lord. Amen."},
	{ID: "quiet-confidence", Title: "For Quiet Confidence", Tradition: Ecumenical, Source: "Book of Common Prayer, 1928",
		Text: "O God of peace, who hast taught us that in returning and rest we shall be saved, in quietness and in confidence shall be our strength; By the might of thy Spirit lift us, we pray thee, to thy presence, where we may be still and know that thou art God; through Jesus Christ our Lord. Amen."},
	{ID: "st-chrysostom", Title: "A Prayer of St. Chrysostom", Tradition: Ecumenical, Source: "Book of Common Prayer, 1928",
		Text: "Almighty God, who hast given us grace at this time with one accord to make our common supplications unto thee; and dost promise that when two or three are gathered together in thy Name thou wilt grant their requests; Fulfil now, O Lord, the desires and petitions of thy servants, as may be most expedient for them; granting us in this world knowledge of thy truth, and in the world to come life everlasting. Amen."},
	{ID: "compline", Title: "For the Night", Tradition: Ecumenical, Source: "Book of Common Prayer, 1979",
		Text: "Keep watch, dear Lord, with those who work, or watch, or weep this night, and give your angels charge over those who sleep. Tend the sick, Lord Christ; give rest to the weary, bless the dying, soothe the suffering, pity the afflicted, shield the joyous; and all for your love's sake. Amen."},
	{ID: "grace-meals", Title: "Grace at Meals", Tradition: Ecumenical, Source: "Book of Common Prayer, 1979",
		Text: "Give us grateful hearts, our Father, for all thy mercies, and make us mindful of the needs of others; through Jesus Christ our Lord. Amen."},
	{ID: "psalm-23", Title: "The Lord Is My Shepherd", Tradition: Ecumenical, Ref: "PSA 23:1-6"},
	{ID: "psalm-51", Title: "A Clean Heart", Tradition: Ecumenical, Ref: "PSA 51:10-12"},
	{ID: "psalm-19", Title: "The Words of My Mouth", Tradition: Ecumenical, Ref: "PSA 19:14"},
	{ID: "psalm-139", Title: "Search Me, O God", Tradition: Ecumenical, Ref: "PSA 139:23-24"},
	{ID: "psalm-121", Title: "My Help Comes from the Lord", Tradition: Ecumenical, Ref: "PSA 121:1-8"},

	{ID: "hail-mary", Title: "Hail Mary", Tradition: Catholic, Source: "Baltimore Catechism, 1891",
		Text: "Hail Mary, full of grace, the Lord is with thee; blessed art thou among women, and blessed is the fruit of thy womb, Jesus. Holy Mary, Mother of God, pray for us sinners, now and at the hour of our death. Amen."},
	{ID: "memorare", Title: "The Memorare", Tradition: Catholic, Source: "Traditional English",
		Text: "Remember, O most gracious Virgin Mary, that never was it known that any one who fled to thy protection, implored thy help, or sought thy intercession, was left unaided. Inspired with this confidence, I fly unto thee, O Virgin of virgins, my Mother; to thee do I come; before thee I stand, sinful and sorrowful. O Mother of the Word Incarnate, despise not my petitions, but in thy mercy hear and answer me. Amen."},
	{ID: "angelus", Title: "The Angelus", Tradition: Catholic, Source: "Traditional English",
		Text: "The Angel of the Lord declared unto Mary, and she conceived of the Holy Ghost. Behold the handmaid of the Lord: be it done unto me according to thy word. And the Word was made flesh, and dwelt among us. Pray for us, O holy Mother of God, that we may be made worthy of the promises of Christ. Pour forth, we beseech thee, O Lord, thy grace into our hearts; that we, to whom the Incarnation of Christ thy Son was made known by the message of an angel, may by his Passion and Cross be brought to the glory of his Resurrection; through the same Christ our Lord. Amen."},
	{ID: "anima-christi", Title: "Anima Christi", Tradition: Catholic, Source: "Traditional English",
		Text: "Soul of Christ, sanctify me. Body of Christ, save me. Blood of Christ, inebriate me. Water from the side of Christ, wash me. Passion of Christ, strengthen me. O good Jesus, hear me. Within thy wounds hide me. Permit me not to be separated from thee. From the malignant enemy defend me. In the hour of my death call me, and bid me come to thee, that with thy saints I may praise thee for ever and ever. Amen."},
	{ID: "act-contrition", Title: "Act of Contrition", Tradition: Catholic, Source: "Baltimore Catechism, 1891",
		Text: "O my God, I am heartily sorry for having offended thee, and I detest all my sins, because I dread the loss of heaven and the pains of hell; but most of all because they offend thee, my God, who art all good and deserving of all my love. I firmly resolve, with the help of thy grace, to confess my sins, to do penance, and to amend my life. Amen."},
	{ID: "st-michael", Title: "Prayer to St. Michael", Tradition: Catholic, Source: "Leo XIII, 1886, traditional English",
		Text: "Saint Michael the Archangel, defend us in battle; be our protection against the wickedness and snares of the devil. May God rebuke him, we humbly pray; and do thou, O Prince of the heavenly host, by the power of God, thrust into hell Satan and all the evil spirits who prowl about the world seeking the ruin of souls. Amen."},

	{ID: "trisagion", Title: "The Trisagion", Tradition: Orthodox, Source: "Traditional English",
		Text: "Holy God, Holy Mighty, Holy Immortal, have mercy on us. Holy God, Holy Mighty, Holy Immortal, have mercy on us. Holy God, Holy Mighty, Holy Immortal, have mercy on us. Glory to the Father, and to the Son, and to the Holy Spirit, now and ever, and unto ages of ages. Amen."},
	{ID: "heavenly-king", Title: "O Heavenly King", Tradition: Orthodox, Source: "Traditional English",
		Text: "O Heavenly King, the Comforter, the Spirit of Truth, who art everywhere present and fillest all things, Treasury of blessings and Giver of life: come and abide in us, and cleanse us from every stain, and save our souls, O Good One."},
	{ID: "st-ephrem", Title: "The Prayer of St. Ephrem", Tradition: Orthodox, Source: "Traditional English",
		Text: "O Lord and Master of my life, take from me the spirit of sloth, faint-heartedness, lust of power, and idle talk. But give rather the spirit of chastity, humility, patience, and love to thy servant. Yea, O Lord and King, grant me to see my own transgressions, and not to judge my brother; for blessed art thou unto ages of ages. Amen."},
}

// PrayerPool is the corpus for a tradition setting: every ecumenical prayer,
// plus the chosen tradition's own.
func PrayerPool(tradition string) []Prayer {
	var out []Prayer
	for _, p := range Prayers {
		if p.Tradition == Ecumenical || p.Tradition == tradition {
			out = append(out, p)
		}
	}
	return out
}

// PrayerByID finds a prayer.
func PrayerByID(id string) (Prayer, bool) {
	for _, p := range Prayers {
		if p.ID == id {
			return p, true
		}
	}
	return Prayer{}, false
}
