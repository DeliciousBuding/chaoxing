package upstream

import "testing"

func TestParseWorkQuestionsBuildsManualForm(t *testing.T) {
	page := `<html><body><form><input name="workId" value="1"><input name="answerq1" value="old"><input name="pyFlag" value=""></form>
	<div class="singleQuesId" data="q1"><div class="TiMu" data="0"></div><div class="Zy_TItle">Pick one</div><ul><li>A</li><li>B</li></ul></div>
	</body></html>`
	set, warnings, err := parseWorkQuestions([]byte(page), WorkJob{ID: "work-1", Title: "Synthetic work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 || len(set.Questions) != 1 {
		t.Fatalf("warnings=%v questions=%+v", warnings, set.Questions)
	}
	question := set.Questions[0]
	if question.ID != "q1" || question.Type != "single" || question.AnswerType != "0" || len(question.Options) != 2 {
		t.Fatalf("%+v", question)
	}
	if set.FormData["workId"] != "1" || set.FormData["answerwqbid"] != "q1," || set.FormData["pyFlag"] != "" {
		t.Fatalf("%+v", set.FormData)
	}
	if _, ok := set.FormData["answerq1"]; ok {
		t.Fatal("stale answer field retained")
	}
}

func TestParseWorkQuestionsRejectsGradedPage(t *testing.T) {
	_, _, err := parseWorkQuestions([]byte(`<html><body>查看已批阅作业</body></html>`), WorkJob{})
	if err == nil || SafeFault(err).Code != "WORK_UNAVAILABLE" {
		t.Fatal(err)
	}
}
