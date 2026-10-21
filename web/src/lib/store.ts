import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { AccountInfo, SessionInfo, ToolDefinition } from "./api";
import { isPageId, type PageId } from "./layout";

export const themes = ["nova-dark", "nova-light", "midnight", "contrast"] as const;
export type Theme = (typeof themes)[number];

export const themeLabels: Record<Theme, string> = {
  "nova-dark": "深色",
  "nova-light": "浅色",
  midnight: "石墨",
  contrast: "高对比"
};

const defaultDashboardOrder = ["security", "egress", "upstream", "watchers", "supervisor", "capabilities"] as const;
export type DashboardCardId = (typeof defaultDashboardOrder)[number];

export function pageFromURL(): PageId | null {
  if (typeof window === "undefined") return null;
  const value = new URL(window.location.href).searchParams.get("page");
  return isPageId(value) ? value : null;
}

export function writePageURL(page: PageId, mode: "push" | "replace" = "push") {
  if (typeof window === "undefined") return;
  const url = new URL(window.location.href);
  if (url.searchParams.get("page") === page) return;
  url.searchParams.set("page", page);
  window.history[mode === "replace" ? "replaceState" : "pushState"](null, "", url);
}

interface UIState {
  session: SessionInfo | null;
  account: AccountInfo | null;
  tools: ToolDefinition[];
  page: PageId;
  theme: Theme;
  dashboardOrder: DashboardCardId[];
  refreshToken: number;
  globalError: string;
  setSession: (session: SessionInfo) => void;
  setAccount: (account: AccountInfo | null) => void;
  setTools: (tools: ToolDefinition[]) => void;
  setPage: (page: PageId) => void;
  setTheme: (theme: Theme) => void;
  setDashboardOrder: (order: DashboardCardId[]) => void;
  refresh: () => void;
  setGlobalError: (error: string) => void;
  resetIdentity: () => void;
}

export const useUIStore = create<UIState>()(
  persist(
    (set) => ({
      session: null,
      account: null,
      tools: [],
      page: "dashboard",
      theme: "nova-dark",
      dashboardOrder: [...defaultDashboardOrder],
      refreshToken: 0,
      globalError: "",
      setSession: (session) => set({ session }),
      setAccount: (account) => set({ account }),
      setTools: (tools) => set({ tools }),
      setPage: (page) => {
        set({ page });
        writePageURL(page);
      },
      setTheme: (theme) => set({ theme }),
      setDashboardOrder: (dashboardOrder) => set({ dashboardOrder }),
      refresh: () => set((state) => ({ refreshToken: state.refreshToken + 1, globalError: "" })),
      setGlobalError: (globalError) => set({ globalError }),
      resetIdentity: () => {
        set({ session: null, account: null, tools: [], page: "dashboard", globalError: "" });
        writePageURL("dashboard", "replace");
      }
    }),
    {
      name: "chaoxing-ui-preferences",
      version: 1,
      partialize: (state) => ({ page: state.page, theme: state.theme, dashboardOrder: state.dashboardOrder })
    }
  )
);
