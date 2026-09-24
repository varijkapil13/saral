package adf_test

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
)

func TestLossyConstructs_NamesOnlyWhatTheDocumentHolds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   string
		opt  adf.Options
		want []string
	}{
		{name: "prose", in: wrap(para(text("nothing special here")))},
		{name: "every mark markdown spells", in: wrap(para(marked("b", "strong") + "," + text(" and ") + "," + marked("c", "code")))},
		{name: "a mention with an account id", in: wrap(para(`{"type":"mention","attrs":{"id":"a1","text":"@A"}}`))},
		{name: "a mention without one", in: wrap(para(`{"type":"mention","attrs":{"text":"@A"}}`)), want: []string{"mention"}},
		{name: "a lozenge", in: wrap(para(`{"type":"status","attrs":{"text":"Done","color":"green"}}`)), want: []string{"status"}},
		{name: "a date", in: wrap(para(`{"type":"date","attrs":{"timestamp":"1"}}`)), want: []string{"date"}},
		{name: "an emoji", in: wrap(para(`{"type":"emoji","attrs":{"shortName":":a:"}}`)), want: []string{"emoji"}},
		{name: "an inline card", in: wrap(para(`{"type":"inlineCard","attrs":{"url":"https://x.test"}}`)), want: []string{"inlineCard"}},
		{name: "an inline attachment", in: wrap(para(`{"type":"mediaInline","attrs":{"id":"1","type":"file"}}`)), want: []string{"media"}},
		{name: "an image block", in: wrap(node("mediaSingle", `"content":[{"type":"media","attrs":{"id":"1","type":"file"}}]`))},
		{name: "a colour", in: wrap(para(`{"type":"text","text":"red","marks":[{"type":"textColor","attrs":{"color":"#f00"}}]}`)), want: []string{"textColor"}},
		{name: "a plain table", in: wrap(headedTable)},
		{name: "a table drawn narrow", in: wrap(headedTable), opt: adf.Options{TableWidth: 20}, want: []string{"table"}},
		{name: "a cell of two paragraphs", in: wrap(node("table", `"content":[`+row(node("tableCell", `"content":[`+para(text("a"))+","+para(text("b"))+`]`))+`]`)), want: []string{"table"}},
		{name: "a cell spanning columns", in: wrap(node("table", `"content":[`+row(node("tableCell", `"attrs":{"colspan":2},"content":[`+para(text("a"))+`]`))+`]`))},
		{name: "a cell spanning rows", in: wrap(node("table", `"content":[`+row(node("tableCell", `"attrs":{"rowspan":2},"content":[`+para(text("a"))+`]`))+`]`)), want: []string{"table"}},
		{name: "a known panel", in: wrap(node("panel", `"attrs":{"panelType":"info","panelColor":"#fff"},"content":[`+para(text("a"))+`]`))},
		{name: "a panel whose type markdown cannot spell", in: wrap(node("panel", `"attrs":{"panelType":"café"},"content":[`+para(text("a"))+`]`)), want: []string{"panel"}},
		{name: "a quote that reads like a panel", in: wrap(node("blockquote", `"content":[`+para(text("ℹ INFO"))+`]`)), want: []string{"blockquote"}},
		{name: "an empty heading", in: wrap(node("heading", `"attrs":{"level":1}`)), want: []string{"heading"}},
		{name: "a hard break between lines", in: wrap(para(text("a") + `,{"type":"hardBreak"},` + text("b")))},
		{name: "a hard break opening a paragraph", in: wrap(para(`{"type":"hardBreak"},` + text("b"))), want: []string{"hardBreak"}},
		{name: "a hard break in a heading", in: wrap(node("heading", `"attrs":{"level":1},"content":[`+text("a")+`,{"type":"hardBreak"},`+text("b")+`]`)), want: []string{"hardBreak"}},
		{name: "a hard break in a cell", in: wrap(node("table", `"content":[`+row(node("tableCell", `"content":[`+para(text("a")+`,{"type":"hardBreak"},`+text("b"))+`]`))+`]`)), want: []string{"hardBreak"}},
		{name: "trailing whitespace", in: wrap(para(text("a  "))), want: []string{"text"}},
		{name: "a control character", in: wrap(para(text("a\\u0007b"))), want: []string{"text"}},
		{name: "an unknown node", in: wrap(node("futureBlock", `"content":[`+para(text("a"))+`]`)), want: []string{"futureBlock"}},
		{name: "what Jira could not parse", in: wrap(node("unsupportedBlock", `"attrs":{"originalValue":{"type":"someMacro"}}`)), want: []string{"someMacro"}},
		{name: "columns", in: wrap(node("layoutSection", `"content":[`+node("layoutColumn", `"content":[`+para(text("a"))+`]`)+`]`)), want: []string{"layoutSection"}},
		{name: "emphasis that reads back", in: wrap(para(marked("bold", "strong") + "," + text("text")))},
		{name: "emphasis that does not", in: wrap(para(marked("checkout 0", "underline") + "," + text("checkout"))), want: []string{"marks"}},
		{
			name: "several, each once, in order",
			in: wrap(para(`{"type":"status","attrs":{"text":"A"}}`+`,{"type":"date","attrs":{"timestamp":"1"}},`+`{"type":"status","attrs":{"text":"B"}}`) + "," +
				para(`{"type":"emoji","attrs":{"shortName":":a:"}}`)),
			want: []string{"status", "date", "emoji"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			losses := adf.LossyConstructs(parse(t, tc.in), tc.opt)
			got := make([]string, 0, len(losses))
			for _, l := range losses {
				if l.Cost == "" {
					t.Errorf("%s names no cost", l.Construct)
				}
				got = append(got, l.Construct)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLossyConstructs_SaysNothingAboutWhatComesBack(t *testing.T) {
	t.Parallel()
	checked := 0
	for name, d := range corpus(t) {
		if len(adf.LossyConstructs(d, adf.Options{})) > 0 {
			continue
		}
		checked++
		md := adf.Markdown(d)
		out, err := adf.ParseMarkdown(md)
		if err != nil {
			t.Errorf("%s: nothing was named and it does not parse: %v", name, err)
			continue
		}
		if again := adf.Markdown(out); again != md {
			t.Errorf("%s: nothing was named and it came back different\n%q\n%q", name, md, again)
		}
	}
	if checked < 10 {
		t.Errorf("only %d documents in the corpus hold nothing lossy, so this proves little", checked)
	}
}

func TestLoss_ReadsAsTheConstructAndItsCost(t *testing.T) {
	t.Parallel()
	if got := (adf.Loss{Construct: "status", Cost: "the colour"}).String(); got != "status: the colour" {
		t.Errorf("got %q", got)
	}
}
