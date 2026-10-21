import * as React from "react";
import { Search } from "lucide-react";
import { api, type ToolDefinition } from "../lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import { Checkbox } from "../components/ui/checkbox";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Select } from "../components/ui/select";
import { Textarea } from "../components/ui/textarea";
import { ErrorBanner, JsonBlock, MetaSummary, PageHeader } from "../components/common";

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
    <div className="space-y-5">
      <PageHeader
        title="工具面板"
        actions={selected ? <Badge>{selected.scope}</Badge> : undefined}
      />
      <ErrorBanner error={error} />
      <div className="grid gap-5 xl:grid-cols-[340px_minmax(0,1fr)]">
      <Card className="h-fit">
        <CardHeader>
          <CardTitle>工具目录</CardTitle>
          <div className="relative pt-2">
            <Search className="absolute left-3 top-5 h-4 w-4 text-muted-foreground" />
            <Input aria-label="搜索工具" className="pl-9" placeholder="搜索工具" value={query} onChange={(event) => setQuery(event.target.value)} />
          </div>
        </CardHeader>
        <CardContent className="max-h-[70vh] space-y-2 overflow-auto">
          {filtered.map((tool) => (
            <button
              key={tool.id}
              onClick={() => choose(tool)}
              className={`w-full rounded-control border p-3 text-left transition-colors ${selected?.id === tool.id ? "border-primary/40 bg-primary/10" : "border-transparent hover:bg-muted"}`}
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
        {selected ? (
          <Card>
            <CardHeader>
              <CardTitle>{selected.title}</CardTitle>
              <CardDescription>{selected.description}</CardDescription>
            </CardHeader>
            <CardContent>
              <form className="space-y-4" onSubmit={run}>
                <div className="grid gap-4 md:grid-cols-2">
                  {Object.entries(selected.fields).map(([name, field]) => {
                    const fieldId = `tool-${selected.id.replace(/[^a-zA-Z0-9_-]/g, "-")}-${name.replace(/[^a-zA-Z0-9_-]/g, "-")}`;
                    return (
                    <div key={name} className="min-w-0 space-y-2">
                      <Label htmlFor={fieldId}>
                        {name}
                        {field.required && <span className="ml-1 text-destructive">*</span>}
                      </Label>
                      {field.enum ? (
                        <Select id={fieldId} value={values[name] ?? ""} onChange={(event) => setValues({ ...values, [name]: event.target.value })}>
                          <option value="">请选择</option>
                          {field.enum.map((item) => <option key={item} value={item}>{item}</option>)}
                        </Select>
                      ) : field.type === "boolean" ? (
                        <label htmlFor={fieldId} className="flex min-h-11 cursor-pointer items-center gap-2 rounded-control border border-border bg-muted/20 px-3 text-[12px] text-muted-foreground transition-colors hover:bg-muted/45">
                          <Checkbox
                            id={fieldId}
                            checked={Boolean(values[name])}
                            onChange={(event) => setValues({ ...values, [name]: event.target.checked })}
                          />
                          <span>{Boolean(values[name]) ? "已启用" : "未启用"}</span>
                        </label>
                      ) : (field.max_length ?? 0) > 256 ? (
                        <Textarea id={fieldId} value={values[name] ?? ""} onChange={(event) => setValues({ ...values, [name]: event.target.value })} />
                      ) : (
                        <Input
                          id={fieldId}
                          type={field.type === "integer" || field.type === "number" ? "number" : field.type}
                          step={field.type === "number" ? "any" : undefined}
                          value={values[name] ?? ""}
                          onChange={(event) => setValues({ ...values, [name]: event.target.value })}
                        />
                      )}
                      <div className="text-xs text-muted-foreground">{field.description}</div>
                    </div>
                    );
                  })}
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
            <CardHeader><CardTitle>执行结果</CardTitle></CardHeader>
            <CardContent className="space-y-3"><MetaSummary data={result} /><JsonBlock value={result} /></CardContent>
          </Card>
        )}
      </div>
      </div>
    </div>
  );
}
