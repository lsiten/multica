export const JEV_DECISION_LOG_SOURCES = [
  "local",
  "agent_context",
  "remote",
  "system_one",
] as const;

export const JEV_DECISION_LOG_RESULTS = [
  "running",
  "success",
  "error",
  "rejected",
] as const;

export const JEV_DECISION_LOG_PAGE_SIZES = [20, 50, 100] as const;

export interface JevDecisionLogFormFilters {
  readonly q: string;
  readonly agent: string;
  readonly source: string;
  readonly result_class: string;
  readonly from: string;
  readonly to: string;
}

export interface JevDecisionLogQueryState {
  readonly filters: JevDecisionLogFormFilters;
  readonly offset: number;
  readonly limit: number;
  readonly asOf: string;
}

export const EMPTY_JEV_DECISION_LOG_FILTERS: JevDecisionLogFormFilters = {
  q: "",
  agent: "",
  source: "",
  result_class: "",
  from: "",
  to: "",
};

const QUERY_PREFIX = "jev_";
const DEFAULT_LIMIT = 20;

function valueIn<T extends string>(
  values: readonly T[],
  value: string,
): string {
  return values.find((candidate) => candidate === value) ?? "";
}

function dateValue(value: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return "";
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const date = new Date(year, month - 1, day);
  return date.getFullYear() === year &&
    date.getMonth() === month - 1 &&
    date.getDate() === day
    ? value
    : "";
}

function nonNegativeInteger(value: string): number | null {
  if (!/^\d+$/.test(value)) return null;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) ? parsed : null;
}

export function jevDecisionLogStateFromParams(
  params: URLSearchParams,
): JevDecisionLogQueryState {
  const requestedLimit = nonNegativeInteger(
    params.get(`${QUERY_PREFIX}limit`) ?? "",
  );
  const limit = JEV_DECISION_LOG_PAGE_SIZES.find(
    (value) => value === requestedLimit,
  ) ?? DEFAULT_LIMIT;
  return {
    filters: {
      q: params.get(`${QUERY_PREFIX}q`) ?? "",
      agent: params.get(`${QUERY_PREFIX}agent`) ?? "",
      source: valueIn(
        JEV_DECISION_LOG_SOURCES,
        params.get(`${QUERY_PREFIX}source`) ?? "",
      ),
      result_class: valueIn(
        JEV_DECISION_LOG_RESULTS,
        params.get(`${QUERY_PREFIX}result`) ?? "",
      ),
      from: dateValue(params.get(`${QUERY_PREFIX}from`) ?? ""),
      to: dateValue(params.get(`${QUERY_PREFIX}to`) ?? ""),
    },
    offset:
      nonNegativeInteger(params.get(`${QUERY_PREFIX}offset`) ?? "") ?? 0,
    limit,
    asOf: params.get(`${QUERY_PREFIX}as_of`) ?? "",
  };
}

export function withJevDecisionLogState(
  current: URLSearchParams,
  state: JevDecisionLogQueryState,
): URLSearchParams {
  const next = new URLSearchParams(current);
  const values: readonly [string, string][] = [
    [`${QUERY_PREFIX}q`, state.filters.q],
    [`${QUERY_PREFIX}agent`, state.filters.agent],
    [
      `${QUERY_PREFIX}source`,
      valueIn(JEV_DECISION_LOG_SOURCES, state.filters.source),
    ],
    [
      `${QUERY_PREFIX}result`,
      valueIn(JEV_DECISION_LOG_RESULTS, state.filters.result_class),
    ],
    [`${QUERY_PREFIX}from`, dateValue(state.filters.from)],
    [`${QUERY_PREFIX}to`, dateValue(state.filters.to)],
    [`${QUERY_PREFIX}offset`, state.offset > 0 ? String(state.offset) : ""],
    [
      `${QUERY_PREFIX}limit`,
      state.limit === DEFAULT_LIMIT ? "" : String(state.limit),
    ],
    [`${QUERY_PREFIX}as_of`, state.asOf],
  ];
  for (const [key, value] of values) {
    if (value) next.set(key, value);
    else next.delete(key);
  }
  return next;
}
