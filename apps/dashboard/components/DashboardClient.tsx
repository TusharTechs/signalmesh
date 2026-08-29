"use client";

import { useQuery } from "@tanstack/react-query";
import { getDashboard } from "@/lib/api";

function Card({
  label,
  value,
  sub,
}: {
  label: string;
  value: string | number;
  sub?: string;
}) {
  return (
    <div className="rounded-xl border border-zinc-800 bg-zinc-900/60 p-4 shadow-sm">
      <div className="text-xs uppercase tracking-wide text-zinc-400">
        {label}
      </div>
      <div className="mt-2 text-2xl font-semibold">{value}</div>
      {sub ? <div className="mt-1 text-xs text-zinc-500">{sub}</div> : null}
    </div>
  );
}

function providerBadgeClass(status: string) {
  switch (status) {
    case "HEALTHY":
      return "bg-emerald-500/15 text-emerald-300 border-emerald-500/30";
    case "DEGRADED":
      return "bg-amber-500/15 text-amber-300 border-amber-500/30";
    case "UNHEALTHY":
      return "bg-red-500/15 text-red-300 border-red-500/30";
    default:
      return "bg-zinc-500/15 text-zinc-300 border-zinc-500/30";
  }
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <h2 className="text-sm font-semibold uppercase tracking-wide text-zinc-300">
      {children}
    </h2>
  );
}

export default function DashboardClient() {
  const { data, error, isLoading } = useQuery({
    queryKey: ["dashboard"],
    queryFn: getDashboard,
    refetchInterval: 2000,
  });

  if (isLoading) {
    return (
      <main className="p-8 text-zinc-300">Loading SignalMesh dashboard...</main>
    );
  }

  if (error || !data) {
    return (
      <main className="p-8">
        <div className="rounded-xl border border-red-500/30 bg-red-500/10 p-4 text-red-200">
          Cannot reach SignalMesh node. Make sure a node is running and
          NEXT_PUBLIC_SIGNALMESH_NODE_URL is correct.
        </div>
      </main>
    );
  }

  const metrics = data.metrics ?? {};
  const cluster = data.cluster ?? {};
  const providers = data.providers ?? {};
  const circuits = data.circuits ?? {};
  const admission = data.admission ?? {};
  const chaos = data.chaos ?? [];
  const decisions = data.recent_decisions ?? [];
  const escalations = data.recent_escalations ?? [];
  const incidents = data.recent_incidents ?? [];
  const providerOutcomes = metrics.provider_outcomes ?? {};

  return (
    <main className="min-h-screen p-6">
      <header className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">SignalMesh</h1>
          <p className="text-sm text-zinc-400">
            Distributed reliability and attention control plane for AI agents
          </p>
        </div>

        <div className="text-right text-sm text-zinc-400">
          <div>Node: {data.node?.node_id ?? "unknown"}</div>
          <div>Cluster size: {cluster.cluster_size ?? 0}</div>
        </div>
      </header>

      <section className="mb-6 grid grid-cols-1 gap-3 md:grid-cols-4">
        <Card label="Requests" value={metrics.requests_total ?? 0} />
        <Card
          label="Successful"
          value={metrics.success_total ?? 0}
          sub={`Failed: ${metrics.failure_total ?? 0}`}
        />
        <Card
          label="p95 latency"
          value={`${metrics.p95_ms ?? 0}ms`}
          sub={`p50: ${metrics.p50_ms ?? 0}ms, p99: ${metrics.p99_ms ?? 0}ms`}
        />
        <Card
          label="Fallbacks"
          value={metrics.fallback_total ?? 0}
          sub={`Semantic failures: ${metrics.semantic_failures_total ?? 0}`}
        />
        <Card label="Escalations" value={metrics.escalations_total ?? 0} />
        <Card label="Incidents" value={metrics.incidents_total ?? 0} />
        <Card
          label="Admission dropped"
          value={metrics.admission_dropped_total ?? 0}
        />
        <Card
          label="Active"
          value={admission.total_active ?? 0}
          sub={`Dropped: ${admission.total_dropped ?? 0}`}
        />
      </section>

      <section className="mb-6 grid grid-cols-1 gap-6 lg:grid-cols-2">
        <div className="rounded-xl border border-zinc-800 bg-zinc-900/40 p-4">
          <SectionTitle>Cluster nodes</SectionTitle>

          <div className="mt-3 overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="text-zinc-500">
                  <th className="py-2 pr-4">Node</th>
                  <th className="py-2 pr-4">Alive</th>
                  <th className="py-2">Last seen</th>
                </tr>
              </thead>
              <tbody>
                {(cluster.nodes ?? []).map((node: any) => (
                  <tr key={node.node_id} className="border-t border-zinc-800">
                    <td className="py-2 pr-4">{node.node_id}</td>
                    <td className="py-2 pr-4">
                      <span
                        className={
                          node.alive
                            ? "text-emerald-300"
                            : "text-red-300"
                        }
                      >
                        {node.alive ? "ALIVE" : "DEAD"}
                      </span>
                    </td>
                    <td className="py-2 text-zinc-400">
                      {new Date(node.last_seen).toLocaleTimeString()}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

        <div className="rounded-xl border border-zinc-800 bg-zinc-900/40 p-4">
          <SectionTitle>Provider health</SectionTitle>

          <div className="mt-3 overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="text-zinc-500">
                  <th className="py-2 pr-4">Provider</th>
                  <th className="py-2 pr-4">Status</th>
                  <th className="py-2 pr-4">Availability</th>
                  <th className="py-2 pr-4">p95</th>
                  <th className="py-2 pr-4">Contract fail</th>
                  <th className="py-2">Circuit</th>
                </tr>
              </thead>
              <tbody>
                {(Object.entries(providers) as [string, any][]).map(
                  ([name, health]) => (
                    <tr key={name} className="border-t border-zinc-800">
                      <td className="py-2 pr-4">{name}</td>
                      <td className="py-2 pr-4">
                        <span
                          className={`rounded-md border px-2 py-1 text-xs ${providerBadgeClass(
                            health.status
                          )}`}
                        >
                          {health.status}
                        </span>
                      </td>
                      <td className="py-2 pr-4">
                        {(health.availability_pct ?? 0).toFixed(1)}%
                      </td>
                      <td className="py-2 pr-4">
                        {health.p95_latency_ms ?? 0}ms
                      </td>
                      <td className="py-2 pr-4">
                        {(health.contract_failure_pct ?? 0).toFixed(1)}%
                      </td>
                      <td className="py-2">{circuits[name] ?? "UNKNOWN"}</td>
                    </tr>
                  )
                )}
              </tbody>
            </table>
          </div>
        </div>
      </section>

      <section className="mb-6 grid grid-cols-1 gap-6 lg:grid-cols-2">
        <div className="rounded-xl border border-zinc-800 bg-zinc-900/40 p-4">
          <SectionTitle>Provider traffic</SectionTitle>

          <div className="mt-3 overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="text-zinc-500">
                  <th className="py-2 pr-4">Provider</th>
                  <th className="py-2 pr-4">Requests</th>
                  <th className="py-2 pr-4">Success</th>
                  <th className="py-2">Failures</th>
                </tr>
              </thead>
              <tbody>
                {(Object.entries(providerOutcomes) as [string, any][]).map(
                  ([name, outcome]) => (
                    <tr key={name} className="border-t border-zinc-800">
                      <td className="py-2 pr-4">{name}</td>
                      <td className="py-2 pr-4">{outcome.requests ?? 0}</td>
                      <td className="py-2 pr-4">{outcome.success ?? 0}</td>
                      <td className="py-2">{outcome.failures ?? 0}</td>
                    </tr>
                  )
                )}
              </tbody>
            </table>
          </div>
        </div>

        <div className="rounded-xl border border-zinc-800 bg-zinc-900/40 p-4">
          <SectionTitle>Active chaos scenarios</SectionTitle>

          {chaos.length === 0 ? (
            <div className="mt-3 text-sm text-zinc-400">
              No active chaos scenarios.
            </div>
          ) : (
            <ul className="mt-3 space-y-2 text-sm">
              {chaos.map((scenario: any) => (
                <li
                  key={`${scenario.run_id}-${scenario.scenario}`}
                  className="rounded-lg border border-zinc-800 bg-zinc-950/60 p-3"
                >
                  <div className="font-medium">{scenario.scenario}</div>
                  <div className="text-xs text-zinc-500">
                    Ends at {new Date(scenario.ends_at).toLocaleTimeString()}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>

      <section className="mb-6 grid grid-cols-1 gap-6 lg:grid-cols-2">
        <div className="rounded-xl border border-zinc-800 bg-zinc-900/40 p-4">
          <SectionTitle>Recent escalations</SectionTitle>

          {escalations.length === 0 ? (
            <div className="mt-3 text-sm text-zinc-400">
              No recent escalations.
            </div>
          ) : (
            <ul className="mt-3 space-y-2 text-sm">
              {escalations.map((esc: any) => (
                <li
                  key={esc.escalation_id}
                  className="rounded-lg border border-zinc-800 bg-zinc-950/60 p-3"
                >
                  <div className="flex items-center justify-between">
                    <span className="font-medium">{esc.phase}</span>
                    <span className="text-xs text-zinc-500">
                      {new Date(esc.created_at).toLocaleTimeString()}
                    </span>
                  </div>

                  <div className="mt-1 text-xs text-zinc-400">
                    {esc.recommended_action}
                  </div>

                  <div className="mt-2 flex flex-wrap gap-1">
                    {(esc.reason_codes ?? []).map((reason: string) => (
                      <span
                        key={reason}
                        className="rounded bg-red-500/10 px-2 py-1 text-xs text-red-300"
                      >
                        {reason}
                      </span>
                    ))}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>

        <div className="rounded-xl border border-zinc-800 bg-zinc-900/40 p-4">
          <SectionTitle>Recent incidents</SectionTitle>

          {incidents.length === 0 ? (
            <div className="mt-3 text-sm text-zinc-400">
              No recent incidents.
            </div>
          ) : (
            <ul className="mt-3 space-y-2 text-sm">
              {incidents.map((inc: any) => (
                <li
                  key={inc.incident_id}
                  className="rounded-lg border border-zinc-800 bg-zinc-950/60 p-3"
                >
                  <div className="flex items-center justify-between">
                    <span className="font-medium">{inc.type}</span>
                    <span className="text-xs text-zinc-500">
                      {new Date(inc.timestamp).toLocaleTimeString()}
                    </span>
                  </div>

                  <div className="mt-1 text-xs text-zinc-400">
                    {inc.reason}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>

      <section className="rounded-xl border border-zinc-800 bg-zinc-900/40 p-4">
        <SectionTitle>Recent decisions</SectionTitle>

        <div className="mt-3 overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="text-zinc-500">
                <th className="py-2 pr-4">Time</th>
                <th className="py-2 pr-4">Request</th>
                <th className="py-2 pr-4">Agent</th>
                <th className="py-2 pr-4">Provider</th>
                <th className="py-2 pr-4">Status</th>
                <th className="py-2 pr-4">Outcome</th>
                <th className="py-2 pr-4">Latency</th>
                <th className="py-2">Reasons</th>
              </tr>
            </thead>
            <tbody>
              {decisions.map((decision: any, idx: number) => (
                <tr key={idx} className="border-t border-zinc-800">
                  <td className="py-2 pr-4 text-zinc-400">
                    {new Date(decision.timestamp).toLocaleTimeString()}
                  </td>
                  <td className="py-2 pr-4">{decision.request_id}</td>
                  <td className="py-2 pr-4">{decision.agent_id || "-"}</td>
                  <td className="py-2 pr-4">{decision.provider || "-"}</td>
                  <td className="py-2 pr-4">{decision.status}</td>
                  <td className="py-2 pr-4">{decision.outcome}</td>
                  <td className="py-2 pr-4">{decision.latency_ms}ms</td>
                  <td className="py-2">
                    <div className="flex max-w-md flex-wrap gap-1">
                      {(decision.reason_codes ?? [])
                        .slice(0, 3)
                        .map((reason: string) => (
                          <span
                            key={reason}
                            className="rounded bg-zinc-800 px-2 py-1 text-xs text-zinc-300"
                          >
                            {reason}
                          </span>
                        ))}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </main>
  );
}