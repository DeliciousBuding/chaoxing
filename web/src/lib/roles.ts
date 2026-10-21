import type { Role } from "./api";

export const roleLabels: Record<Role, string> = {
  owner: "所有者",
  admin: "管理员",
  operator: "操作员",
  viewer: "只读"
};

export const roleDescriptions: Record<Role, string> = {
  owner: "全部账号、成员与审计；唯一超级管理员",
  admin: "管理成员与全部账号；可使用全部业务工具",
  operator: "仅授权账号；签到、答题、草稿与上传",
  viewer: "仅授权账号；只读工具，不读取签到 IP/地址"
};

export const assignableRoles: Record<Role, Role[]> = {
  owner: ["admin", "operator", "viewer"],
  admin: ["operator", "viewer"],
  operator: [],
  viewer: []
};
