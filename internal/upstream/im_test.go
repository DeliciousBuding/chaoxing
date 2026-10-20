package upstream

import "testing"

func TestParseIMAttachmentNormalizesSignCard(t *testing.T) {
	item, ok := parseIMAttachment(`{"attachmentType":15,"att_chat_course":{"aid":9001,"atype":2,"atypeName":"密码签到","title":"课堂签到","subTitle":"","courseInfo":{"courseid":123,"classid":456,"coursename":"Synthetic Course"}}}`)
	if !ok {
		t.Fatal("sign card rejected")
	}
	if item.ID != "9001" || item.CourseID != "123" || item.ClassID != "456" || item.Course != "Synthetic Course" {
		t.Fatalf("%+v", item)
	}
	if item.Kind != "signin" || !item.Signable || item.SignType != "password" || item.RawOtherID != 5 {
		t.Fatalf("%+v", item)
	}
}

func TestParseIMAttachmentRejectsNonCourseCard(t *testing.T) {
	if _, ok := parseIMAttachment(`{"attachmentType":14}`); ok {
		t.Fatal("non-course attachment accepted")
	}
	if _, ok := parseIMAttachment(`{"attachmentType":15,"att_chat_course":{"aid":0,"atype":2}}`); ok {
		t.Fatal("card without activity id accepted")
	}
}
