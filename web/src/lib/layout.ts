import {
  Activity,
  BookOpen,
  Brain,
  CalendarDays,
  ClipboardList,
  GraduationCap,
  LayoutDashboard,
  MapPin,
  ScrollText,
  Shield,
  Sparkles,
  Users,
  Wrench,
  type LucideIcon
} from "lucide-react";
import type { Capabilities } from "./api";

export const pages = [
  { id: "dashboard", title: "总览", icon: LayoutDashboard, group: "core" },
  { id: "courses", title: "课程", icon: BookOpen, group: "core" },
  { id: "todo", title: "待办", icon: ClipboardList, group: "core" },
  { id: "activities", title: "课堂", icon: Activity, group: "core" },
  { id: "study", title: "课表", icon: CalendarDays, group: "core" },
  { id: "work", title: "作业答题", icon: Brain, group: "core", requirement: "work" as const, hidden: true },
  { id: "notices", title: "通知", icon: GraduationCap, group: "core", hidden: true },
  { id: "exams", title: "考试", icon: ClipboardList, group: "core", hidden: true },
  { id: "captures", title: "生产捕获", icon: Sparkles, group: "ops", requirement: "sensitive" as const, hidden: true },
  { id: "locations", title: "位置", icon: MapPin, group: "ops", requirement: "accounts" as const },
  { id: "tools", title: "工具面板", icon: Wrench, group: "ops", requirement: "sensitive" as const, hidden: true },
  { id: "members", title: "成员", icon: Users, group: "admin", requirement: "manage_users" as const },
  { id: "audit", title: "审计", icon: ScrollText, group: "admin", requirement: "view_audit" as const },
  { id: "settings", title: "设置", icon: Shield, group: "admin" }
] as const;

export type PageId = (typeof pages)[number]["id"];
export type PageGroup = (typeof pages)[number]["group"];

export function isPageId(value: string | null | undefined): value is PageId {
  return pages.some((page) => page.id === value);
}

export const pageGroups: Array<{ id: PageGroup; title: string }> = [
  { id: "core", title: "学习" },
  { id: "ops", title: "运行" },
  { id: "admin", title: "管理" }
];

export function pageMeta(id: PageId) {
  return pages.find((page) => page.id === id) ?? pages[0];
}

export function visiblePages() {
  return pages.filter((page) => !("hidden" in page && page.hidden));
}

export function pageIcon(id: PageId): LucideIcon {
  return pageMeta(id).icon;
}

export function canAccessPage(id: PageId, capabilities?: Capabilities) {
  const page = pages.find((item) => item.id === id);
  const requirement = page && "requirement" in page ? page.requirement : undefined;
  if (!requirement) return true;
  if (!capabilities) return false;
  if (requirement === "manage_users") return capabilities.manage_users;
  if (requirement === "view_audit") return capabilities.view_audit;
  if (requirement === "sensitive") return capabilities.sensitive_read;
  if (requirement === "accounts") return capabilities.manage_accounts;
  if (requirement === "work") return capabilities.tool_scopes?.includes("chaoxing.work") ?? false;
  return true;
}
