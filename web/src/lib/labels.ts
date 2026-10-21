const labels = {
  actionKind: {
    assignment: "作业",
    exam: "考试",
    notice: "通知"
  },
  action: {
    review_assignment: "待完成",
    review_state: "核对状态",
    review_exam: "核对考试",
    read_notice: "阅读通知",
    pending: "待处理"
  },
  submission: {
    pending: "待提交",
    submitted: "已提交",
    unknown: "未知"
  },
  availability: {
    open: "可作答",
    closed: "已关闭",
    not_open: "未开放",
    unknown: "未知"
  },
  activityKind: {
    signin: "签到",
    sign: "签到",
    answer: "答题",
    quiz: "测验",
    practice: "随堂练习",
    homework: "作业",
    activity: "活动"
  },
  activityState: {
    active: "进行中",
    ended: "已结束",
    expired: "已过期",
    unknown: "未知"
  },
  userState: {
    unsigned: "未参与",
    pending: "待确认",
    signed: "已签到",
    teacher_signed: "教师代签",
    leave: "请假",
    absence: "缺勤",
    late: "迟到",
    leave_early: "早退",
    expired: "已失效",
    unknown: "未知"
  },
  signType: {
    normal_or_photo: "普通/拍照",
    qrcode: "二维码",
    gesture: "手势",
    location: "定位",
    password: "密码",
    photo: "拍照",
    face: "人脸",
    unknown: "未知"
  },
  chapterState: {
    completed: "已完成",
    pending: "进行中",
    unknown: "未知"
  },
  questionType: {
    single: "单选",
    multiple: "多选",
    completion: "填空",
    judgement: "判断",
    shortanswer: "简答",
    unknown: "未知"
  },
  noticeSource: {
    notice: "通知",
    announcement: "公告",
    work: "作业",
    exam: "考试"
  }
} as const;

export type LabelDomain = keyof typeof labels;

export function labelFor(domain: LabelDomain, value: unknown): string {
  const key = typeof value === "string" ? value : "";
  return (labels[domain] as Record<string, string>)[key] ?? (key || "未知");
}
