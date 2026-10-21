import {
  Activity,
  BookOpen,
  Brain,
  CalendarDays,
  ClipboardList,
  GraduationCap,
  LayoutDashboard,
  MapPin,
  Shield,
  Sparkles,
  Wrench,
  type LucideIcon
} from "lucide-react";

export const pages = [
  { id: "dashboard", title: "总览", icon: LayoutDashboard, group: "core" },
  { id: "courses", title: "课程", icon: BookOpen, group: "core" },
  { id: "todo", title: "待办", icon: ClipboardList, group: "core" },
  { id: "study", title: "课表与资料", icon: CalendarDays, group: "study" },
  { id: "activities", title: "课堂活动", icon: Activity, group: "study" },
  { id: "work", title: "作业答题", icon: Brain, group: "study" },
  { id: "notices", title: "通知", icon: GraduationCap, group: "study" },
  { id: "exams", title: "考试", icon: ClipboardList, group: "study" },
  { id: "captures", title: "生产捕获", icon: Sparkles, group: "ops" },
  { id: "locations", title: "位置预设", icon: MapPin, group: "ops" },
  { id: "tools", title: "工具面板", icon: Wrench, group: "ops" },
  { id: "settings", title: "安全与配置", icon: Shield, group: "system" }
] as const;

export type PageId = (typeof pages)[number]["id"];
export type PageGroup = (typeof pages)[number]["group"];

export const pageGroups: Array<{ id: PageGroup; title: string }> = [
  { id: "core", title: "核心" },
  { id: "study", title: "学业" },
  { id: "ops", title: "运行" },
  { id: "system", title: "系统" }
];

export function pageMeta(id: PageId) {
  return pages.find((page) => page.id === id) ?? pages[0];
}

export function pageIcon(id: PageId): LucideIcon {
  return pageMeta(id).icon;
}
