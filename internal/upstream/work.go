package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	knowledgeCardsAPI = "https://mooc1.chaoxing.com/mooc-ans/knowledge/cards"
	workQuestionsAPI  = "https://mooc1.chaoxing.com/mooc-ans/api/work"
)

var mArgPattern = regexp.MustCompile(`mArg=\{(.*?)\};`)

type WorkJob struct {
	ID          string `json:"id"`
	CourseID    string `json:"course_id"`
	ClassID     string `json:"class_id"`
	KnowledgeID string `json:"knowledge_id"`
	Knowledge   string `json:"knowledge"`
	Title       string `json:"title"`
	Enc         string `json:"-"`
	KToken      string `json:"-"`
	CPI         string `json:"-"`
	Type        string `json:"type"`
	IsPassed    bool   `json:"is_passed"`
}

type WorkQuestion struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	AnswerType string   `json:"answer_type"`
	Options    []string `json:"options"`
}

type WorkQuestionSet struct {
	Job       WorkJob           `json:"job"`
	Questions []WorkQuestion    `json:"questions"`
	FormData  map[string]string `json:"form_data"`
	Warnings  []string          `json:"warnings"`
	FetchedAt time.Time         `json:"fetched_at"`
}

// ListWorkJobs reads course chapter points and their knowledge-card catalog,
// then returns only answer-work cards. Job references are cached in memory so
// a later question read cannot mix parameters from another course.
func (c *Client) ListWorkJobs(ctx context.Context, args map[string]any) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	courseID, classID, cpi := str(args, "course_id"), str(args, "class_id"), str(args, "cpi")
	courses, err := c.fetchCourses(ctx)
	if err != nil {
		return nil, err
	}
	var course Course
	found := false
	for _, item := range courses {
		if item.ID == courseID && item.ClassID == classID && item.CPI == cpi {
			course, found = item, true
			break
		}
	}
	if !found {
		return nil, fault("COURSE_ACCESS", "course triple is not enrolled")
	}
	points, err := c.fetchKnowledgePoints(ctx, course)
	if err != nil {
		return nil, err
	}
	if len(points) > 50 {
		points = points[:50]
	}
	jobs := []WorkJob{}
	warnings := []string{}
	for _, point := range points {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows, err := c.fetchKnowledgeWorkJobs(ctx, course, point)
		if err != nil {
			warnings = append(warnings, "knowledge "+point.ID+": "+academicCode(err))
			continue
		}
		jobs = append(jobs, rows...)
		if len(jobs) >= 50 {
			jobs = jobs[:50]
			warnings = append(warnings, "work job output capped at 50")
			break
		}
	}
	if c.workJobs == nil {
		c.workJobs = map[string]WorkJob{}
	}
	for _, job := range jobs {
		c.workJobs[workJobKey(job)] = job
	}
	return map[string]any{
		"items": jobs, "returned": len(jobs), "knowledge_points_checked": len(points),
		"complete": len(warnings) == 0 && len(points) <= 50, "coverage": "course_knowledge_cards",
		"warnings": warnings, "fetched_at": time.Now().UTC(),
	}, nil
}

// WorkQuestions reads the editable question page for one cached work job. It
// never posts answers or enters an exam.
func (c *Client) WorkQuestions(ctx context.Context, args map[string]any) (any, error) {
	set, err := c.GetWorkQuestions(ctx, str(args, "course_id"), str(args, "class_id"), str(args, "job_id"))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"job": set.Job, "questions": set.Questions, "form_data": set.FormData,
		"warnings": set.Warnings, "complete": true, "coverage": "single_work_question_page",
		"fetched_at": set.FetchedAt,
	}, nil
}

func (c *Client) GetWorkQuestions(ctx context.Context, courseID, classID, jobID string) (WorkQuestionSet, error) {
	job, ok := c.workJobs[workJobKey(WorkJob{CourseID: courseID, ClassID: classID, ID: jobID})]
	if !ok {
		return WorkQuestionSet{}, fault("NOT_FOUND", "work job is not in the current cache; call list_work_jobs first")
	}
	q := url.Values{
		"api": {"1"}, "workId": {strings.TrimPrefix(job.ID, "work-")}, "jobid": {job.ID},
		"originJobId": {job.ID}, "needRedirect": {"true"}, "skipHeader": {"true"},
		"knowledgeid": {job.KnowledgeID}, "ktoken": {job.KToken}, "cpi": {job.CPI},
		"ut": {"s"}, "clazzId": {job.ClassID}, "type": {""}, "enc": {job.Enc},
		"mooc2": {"1"}, "courseid": {job.CourseID},
	}
	b, err := c.get(ctx, workQuestionsAPI+"?"+q.Encode())
	if err != nil {
		return WorkQuestionSet{}, err
	}
	set, warnings, err := parseWorkQuestions(b, job)
	if err != nil {
		return WorkQuestionSet{}, err
	}
	set.Warnings = warnings
	return set, nil
}

type knowledgePoint struct {
	ID    string
	Title string
}

func (c *Client) fetchKnowledgePoints(ctx context.Context, course Course) ([]knowledgePoint, error) {
	q := url.Values{"courseid": {course.ID}, "clazzid": {course.ClassID}, "cpi": {course.CPI}, "ut": {"s"}}
	b, err := c.chapterGET(ctx, "https://mooc2-ans.chaoxing.com/mooc2-ans/mycourse/studentcourse?"+q.Encode())
	if err != nil {
		return nil, err
	}
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return nil, fault("UPSTREAM_SCHEMA", "chapter catalog not recognized")
	}
	points := []knowledgePoint{}
	walk(doc, func(n *html.Node) {
		if n.Data != "div" || !hasClass(n, "chapter_item") || !strings.HasPrefix(attr(n, "id"), "cur") {
			return
		}
		title := attr(n, "title")
		if title == "" {
			if node := first(n, func(v *html.Node) bool { return hasClass(v, "catalog_name") }); node != nil {
				title = text(node)
			}
		}
		points = append(points, knowledgePoint{ID: strings.TrimPrefix(attr(n, "id"), "cur"), Title: chapterShort(title)})
	})
	if len(points) == 0 {
		return nil, fault("UPSTREAM_SCHEMA", "course has no recognizable knowledge points")
	}
	return points, nil
}

func (c *Client) fetchKnowledgeWorkJobs(ctx context.Context, course Course, point knowledgePoint) ([]WorkJob, error) {
	q := url.Values{
		"clazzid": {course.ClassID}, "courseid": {course.ID}, "knowledgeid": {point.ID},
		"ut": {"s"}, "cpi": {course.CPI}, "v": {"2025-0424-1038-3"}, "mooc2": {"1"}, "num": {"0"},
	}
	b, err := c.get(ctx, knowledgeCardsAPI+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	compact := strings.ReplaceAll(string(b), " ", "")
	m := mArgPattern.FindStringSubmatch(compact)
	if m == nil {
		return nil, fault("UPSTREAM_SCHEMA", "knowledge cards not recognized")
	}
	var payload struct {
		Attachments []struct {
			JobID      any    `json:"jobid"`
			ID         any    `json:"id"`
			Type       string `json:"type"`
			Enc        string `json:"enc"`
			OtherInfo  string `json:"otherinfo"`
			OtherInfo2 string `json:"otherInfo"`
			IsPassed   bool   `json:"isPassed"`
			Property   struct {
				Name  string `json:"name"`
				Title string `json:"title"`
				Type  string `json:"type"`
			} `json:"property"`
		} `json:"attachments"`
		Defaults struct {
			KnowledgeID any `json:"knowledgeid"`
			KToken      any `json:"ktoken"`
			CPI         any `json:"cpi"`
		} `json:"defaults"`
		KnowledgeName string `json:"knowledgename"`
	}
	if json.Unmarshal([]byte("{"+m[1]+"}"), &payload) != nil {
		return nil, fault("UPSTREAM_SCHEMA", "knowledge cards payload not recognized")
	}
	jobs := []WorkJob{}
	for _, row := range payload.Attachments {
		jobType := strings.ToLower(firstNonBlank(row.Type, row.Property.Type))
		if jobType != "workid" {
			continue
		}
		jobID := firstNonBlank(id(row.JobID), id(row.ID))
		if jobID == "" {
			return nil, fault("UPSTREAM_SCHEMA", "work card has no stable job id")
		}
		other := firstNonBlank(row.OtherInfo, row.OtherInfo2)
		if strings.Contains(other, "&") {
			other = strings.Split(other, "&")[0]
		}
		jobs = append(jobs, WorkJob{
			ID: jobID, CourseID: course.ID, ClassID: course.ClassID,
			KnowledgeID: firstNonBlank(id(payload.Defaults.KnowledgeID), point.ID),
			Knowledge:   firstNonBlank(payload.KnowledgeName, point.Title), Title: chapterShort(firstNonBlank(row.Property.Name, row.Property.Title)),
			Enc: row.Enc, KToken: id(payload.Defaults.KToken), CPI: firstNonBlank(id(payload.Defaults.CPI), course.CPI),
			Type: "work", IsPassed: row.IsPassed,
		})
	}
	return jobs, nil
}

func parseWorkQuestions(b []byte, job WorkJob) (WorkQuestionSet, []string, error) {
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return WorkQuestionSet{}, nil, fault("UPSTREAM_SCHEMA", "work question page not recognized")
	}
	pageText := text(doc)
	switch {
	case strings.Contains(pageText, "查看已批阅作业"), strings.Contains(pageText, "已批阅"):
		return WorkQuestionSet{}, nil, fault("WORK_UNAVAILABLE", "work is already graded")
	case strings.Contains(pageText, "教师未创建完成"), strings.Contains(pageText, "未创建完成"):
		return WorkQuestionSet{}, nil, fault("WORK_UNAVAILABLE", "work is not created")
	case strings.Contains(pageText, "作业未开放"), strings.Contains(pageText, "尚未开放"):
		return WorkQuestionSet{}, nil, fault("WORK_UNAVAILABLE", "work is not open")
	case strings.Contains(pageText, "作业已经结束"), strings.Contains(pageText, "考试已经结束"):
		return WorkQuestionSet{}, nil, fault("WORK_UNAVAILABLE", "work has ended")
	}
	warnings := []string{}
	if strings.Contains(string(b), "cxSecretStyle") {
		warnings = append(warnings, "upstream font obfuscation detected; verify decoded titles manually")
	}
	formData := map[string]string{}
	walk(doc, func(n *html.Node) {
		if n.Data != "input" {
			return
		}
		name := attr(n, "name")
		if name == "" || strings.HasPrefix(name, "answer") {
			return
		}
		formData[name] = attr(n, "value")
	})
	questions := []WorkQuestion{}
	walk(doc, func(n *html.Node) {
		if n.Data != "div" || !hasClass(n, "singleQuesId") {
			return
		}
		qid := attr(n, "data")
		if qid == "" {
			return
		}
		typeCode := ""
		if tm := first(n, func(v *html.Node) bool { return hasClass(v, "TiMu") }); tm != nil {
			typeCode = attr(tm, "data")
		}
		title := ""
		if node := first(n, func(v *html.Node) bool { return hasClass(v, "Zy_TItle") }); node != nil {
			title = chapterShort(text(node))
		}
		options := []string{}
		if ul := first(n, func(v *html.Node) bool { return v.Data == "ul" }); ul != nil {
			for li := ul.FirstChild; li != nil; li = li.NextSibling {
				if li.Data != "li" {
					continue
				}
				if option := strings.TrimSpace(firstNonBlank(attr(li, "aria-label"), text(li))); option != "" {
					options = append(options, chapterShort(option))
				}
			}
		}
		questions = append(questions, WorkQuestion{ID: qid, Title: title, Type: workQuestionType(typeCode), AnswerType: typeCode, Options: options})
	})
	if len(questions) == 0 {
		return WorkQuestionSet{}, nil, fault("UPSTREAM_SCHEMA", "work page has no recognizable editable questions")
	}
	ids := make([]string, 0, len(questions))
	for _, question := range questions {
		ids = append(ids, question.ID)
	}
	formData["answerwqbid"] = strings.Join(ids, ",") + ","
	if _, ok := formData["pyFlag"]; !ok {
		formData["pyFlag"] = ""
	}
	return WorkQuestionSet{Job: job, Questions: questions, FormData: formData, FetchedAt: time.Now().UTC()}, warnings, nil
}

func workQuestionType(code string) string {
	switch strings.TrimSpace(code) {
	case "0":
		return "single"
	case "1":
		return "multiple"
	case "2":
		return "completion"
	case "3":
		return "judgement"
	case "4":
		return "shortanswer"
	default:
		return "unknown"
	}
}

func workJobKey(job WorkJob) string {
	return job.CourseID + ":" + job.ClassID + ":" + job.ID
}
