import * as React from "react";
import { Search } from "lucide-react";
import { api, type ToolDefinition } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Select } from "../components/ui/select";
import { Textarea } from "../components/ui/textarea";
import { ErrorBanner, JsonBlock, MetaSummary } from "../components/common";

export function Tools({ tools }: { tools: ToolDefinition[] }) {
  const [query, setQuery] = React.useState("");
  const [selected, setSelected] = React.useState<ToolDefinition | null>(null);
  const [values, setValues] = React.useState<Record<string, any>>({});
  const [result, setResult] = React.useState<any>(null);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  const filtered = tools.filter((tool) =>
    !query || `${tool.title} ${tool.name} ${tool.description}`.toLowerCase().includes(query.toLowerCase())
  );

  function choose(tool: ToolDefinition) {
    setSelected(tool);
    setResult(null);
    setError("");
    const initial: Record<string, any> = {};
    for (const [name, field] of Object.entries(tool.fields)) {
      initial[name] = field.type === "boolean" ? false : "";
    }
    setValues(initial);
  }

  async function run(event: React.FormEvent) {
    event.preventDefault();
    if (!selected) return;
    const args: Record<string, any> = {};
    setError("");
    for (const [name, field] of Object.entries(selected.fields)) {
      let value = values[name] ?? "";
      if (field.type === "integer" || field.type === "number") {
        if (value === "" && field.required) {
          setError(`参数 ${name} 不能为空`);
          return;
        }
        if (value === "") continue;
        value = Number(value);
        if (!Number.isFinite(value)) {
          setError(`参数 ${name} 不是有效数字`);
          return;
        }
      } else if (field.type === "boolean") {
        value = Boolean(value);
      } else {
        value = String(value).trim();
        if (value === "") {
          if (field.required) {
            setError(`参数 ${name} 不能为空`);
            return;
          }
          continue;
        }
      }
      args[name] = value;
    }
    setBusy(true);
    setError("");
    try { setResult((await api.call(selected.name, args)).data); }
    catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }

  return (
    <div className="grid gap-5 xl:grid-cols-[360px_1fr]">
      <Card className="h-fit">
        <CardHeader>
          <CardTitle>工具面板</CardTitle>
          <CardDescription>所有工具均来自后端 allowlist，不提供任意 URL 或命令执行。</CardDescription>
          <div className="relative pt-2">
            <Search className="absolute left-3 top-5 h-4 w-4 text-muted-foreground" />
            <Input className="pl-9" placeholder="搜索工具" value={query} onChange={(event) => setQuery(event.target.value)} />
          </div>
        </CardHeader>
        <CardContent className="max-h-[70vh] space-y-2 overflow-auto">
          {filtered.map((tool) => (
            <button
              key={tool.id}
              onClick={() => choose(tool)}
              className={`w-full rounded-xl border p-3 text-left transition ${selected?.id === tool.id ? "border-primary/50 bg-primary/10" : "hover:bg-muted"}`}
            >
              <div className="flex items-center justify-between gap-2">
                <span className="text-sm font-medium">{tool.title}</span>
                <Badge variant="secondary">{tool.scope}</Badge>
              </div>
              <div className="mt-1 line-clamp-2 text-xs text-muted-foreground">{tool.description}</div>
            </button>
          ))}
        </CardContent>
      </Card>

      <div className="space-y-5">
        <ErrorBanner error={error} />
        {selected ? (
          <Card>
            <CardHeader>
              <CardTitle>{selected.title}</CardTitle>
              <CardDescription>{selected.description}</CardDescription>
            </CardHeader>
            <CardContent>
              <form className="space-y-4" onSubmit={run}>
                <div className="grid gap-4 md:grid-cols-2">
                  {Object.entries(selected.fields).map(([name, field]) => (
                    <div key={name} className="space-y-2">
                      <Label>
                        {name}
                        {field.required && <span className="ml-1 text-destructive">*</span>}
                      </Label>
                      {field.enum ? (
                        <Select value={values[name] ?? ""} onChange={(event) => setValues({ ...values, [name]: event.target.value })}>
                          <option value="">请选择</option>
                          {field.enum.map((item) => <option key={item} value={item}>{item}</option>)}
                        </Select>
                      ) : field.type === "boolean" ? (
                        <input
                          type="checkbox"
                          className="h-4 w-4 rounded border-input"
                          checked={Boolean(values[name])}
                          onChange={(event) => setValues({ ...values, [name]: event.target.checked })}
                        />
                      ) : (field.max_length ?? 0) > 256 ? (
                        <Textarea value={values[name] ?? ""} onChange={(event) => setValues({ ...values, [name]: event.target.value })} />
                      ) : (
                        <Input
                          type={field.type === "integer" || field.type === "number" ? "number" : field.type}
                          step={field.type === "number" ? "any" : undefined}
                          value={values[name] ?? ""}
                          onChange={(event) => setValues({ ...values, [name]: event.target.value })}
                        />
                      )}
                      <div className="text-xs text-muted-foreground">{field.description}</div>
                    </div>
                  ))}
                </div>
                <Button type="submit" disabled={busy}>{busy ? "执行中…" : "执行工具"}</Button>
              </form>
            </CardContent>
          </Card>
        ) : (
          <Card><CardContent className="p-10 text-center text-sm text-muted-foreground">选择左侧工具开始操作</CardContent></Card>
        )}
        {result && (
          <Card>
            <CardHeader><CardTitle>执行结果</CardTitle><CardDescription>结构化返回，包含覆盖范围与 trace_id。</CardDescription></CardHeader>
            <CardContent className="space-y-3"><MetaSummary data={result} /><JsonBlock value={result} /></CardContent>
          </Card>
        )}
      </div>
    </div>
  );
}
