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
  { id: "study", title: "课表与资料", icon: CalendarDays, group: "study" },
  { id: "activities", title: "课堂活动", icon: Activity, group: "study" },
  { id: "work", title: "作业答题", icon: Brain, group: "study", requirement: "work" as const },
  { id: "notices", title: "通知", icon: GraduationCap, group: "study" },
  { id: "exams", title: "考试", icon: ClipboardList, group: "study" },
  { id: "captures", title: "生产捕获", icon: Sparkles, group: "ops", requirement: "sensitive" as const },
  { id: "locations", title: "位置预设", icon: MapPin, group: "ops", requirement: "accounts" as const },
  { id: "tools", title: "工具面板", icon: Wrench, group: "ops" },
  { id: "members", title: "成员与权限", icon: Users, group: "admin", requirement: "manage_users" as const },
  { id: "audit", title: "审计日志", icon: ScrollText, group: "admin", requirement: "view_audit" as const },
  { id: "settings", title: "安全与配置", icon: Shield, group: "system" }
] as const;

export type PageId = (typeof pages)[number]["id"];
export type PageGroup = (typeof pages)[number]["group"];

export function isPageId(value: string | null | undefined): value is PageId {
  return pages.some((page) => page.id === value);
}

export const pageGroups: Array<{ id: PageGroup; title: string }> = [
  { id: "core", title: "核心" },
  { id: "study", title: "学业" },
  { id: "ops", title: "运行" },
  { id: "admin", title: "管理" },
  { id: "system", title: "系统" }
];

export function pageMeta(id: PageId) {
  return pages.find((page) => page.id === id) ?? pages[0];
}

export function pageIcon(id: PageId): LucideIcon {
  return pageMeta(id).icon;
}

export function canAccessPage(id: PageId, capabilities?: Capabilities) {
  if (!capabilities) return true;
  const page = pages.find((item) => item.id === id);
  const requirement = page && "requirement" in page ? page.requirement : undefined;
  if (!requirement) return true;
  if (requirement === "manage_users") return capabilities.manage_users;
  if (requirement === "view_audit") return capabilities.view_audit;
  if (requirement === "sensitive") return capabilities.sensitive_read;
  if (requirement === "accounts") return capabilities.manage_accounts;
  if (requirement === "work") return capabilities.tool_scopes.includes("chaoxing.work");
  return true;
}
