export const SIGNALMESH_NODE_URL =
  process.env.NEXT_PUBLIC_SIGNALMESH_NODE_URL ?? "http://localhost:8080";

export async function getDashboard(): Promise<any> {
  const res = await fetch(`${SIGNALMESH_NODE_URL}/api/dashboard`, {
    cache: "no-store",
  });

  if (!res.ok) {
    throw new Error(`Failed to fetch dashboard: ${res.status}`);
  }

  return res.json();
}