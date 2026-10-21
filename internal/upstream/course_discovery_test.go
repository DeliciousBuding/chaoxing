package upstream

import "testing"

func TestParseCourseListPage(t *testing.T) {
	page := `<html><body><div class="course-list">
	  <div class="course" id="course_1">
	    <input class="clazzId" value="1001">
	    <input class="courseId" value="2002">
	    <span class="course-name" title="操作系统"></span>
	    <p class="color3" title="周军海"></p>
	    <a href="/mooc2-ans/mycourse/studentcourse?courseid=2002&clazzid=1001&cpi=9">进入</a>
	  </div>
	  <div class="course" id="course_2">
	    <input class="clazzid" value="1003">
	    <input class="courseid" value="2004">
	    <span class="course-name" title="模式识别"></span>
	    <p class="color3" title="杨胜"></p>
	    <div class="not-open-tip">未开放</div>
	  </div>
	  <div class="course"><span class="course-name">placeholder</span></div>
	</div></body></html>`
	rows := parseCourseListPage(page)
	if len(rows) != 2 {
		t.Fatalf("rows=%v", rows)
	}
	if rows[0].CourseID != "2002" || rows[0].ClassID != "1001" || rows[0].Name != "操作系统" || rows[0].Teacher != "周军海" {
		t.Fatal(rows[0])
	}
	if rows[1].Status != "未开放" {
		t.Fatal(rows[1])
	}
}

func TestParseCourseListPageRejectsEmpty(t *testing.T) {
	if rows := parseCourseListPage(""); len(rows) != 0 {
		t.Fatal(rows)
	}
}
