package upstream

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestParseMaterialRows(t *testing.T) {
	const page = `
		<ul class="dataBody_td" type="tch-courseware">
			<li class="dataBody_name_stu"><dl><dt>教师课件</dt></dl></li>
		</ul>
		<ul class="dataBody_td" id="41" objectid="object-41" dataname="lecture-01.pdf" type="pdf" isdown="1">
			<li>lecture-01.pdf</li><li>2.4MB</li><li>2026-10-20</li>
		</ul>
		<ul class="dataBody_td" id="42" dataname="week-1" type="afolder" isdown="0">
			<li>week-1</li>
		</ul>
	`
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	rows := parseMaterialRows(doc)
	if len(rows) != 3 {
		t.Fatalf("rows=%d %+v", len(rows), rows)
	}
	if rows[0].Name != "教师课件" || !rows[0].IsFolder || rows[0].FolderKind != "tch-courseware" {
		t.Fatalf("courseware row=%+v", rows[0])
	}
	if rows[1].Name != "lecture-01.pdf" || rows[1].Size != "2.4MB" || rows[1].CreatedAt != "2026-10-20" || !rows[1].Downloadable {
		t.Fatalf("file row=%+v", rows[1])
	}
	if !rows[2].IsFolder || rows[2].FolderKind != "afolder" || rows[2].Downloadable {
		t.Fatalf("folder row=%+v", rows[2])
	}
}
