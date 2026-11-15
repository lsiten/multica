import { useCallback, useState } from "react";
import { useNavigation, useOptionalNavigation } from "./context";
import type { NavigationAdapter } from "./types";

// Router replacements are transitions, so two controls on the same page can
// update before the adapter receives the first URL. Keep their shared pending
// parameters until that navigation commits; otherwise the second control
// rebuilds from the previous render and silently drops the first update.
const pendingSearchParams = new WeakMap<
  NavigationAdapter,
  URLSearchParams
>();

function latestSearchParams(navigation: NavigationAdapter): URLSearchParams {
  const pending = pendingSearchParams.get(navigation);
  if (pending) return pending;
  const initial = new URLSearchParams(navigation.searchParams);
  pendingSearchParams.set(navigation, initial);
  return initial;
}

export function useSearchState(key: string, defaultValue = "") {
  const navigation = useNavigation();
  const value = navigation.searchParams.get(key) ?? defaultValue;
  const setValue = useCallback((next: string) => {
    const params = new URLSearchParams(latestSearchParams(navigation));
    if (!next || next === defaultValue) params.delete(key);
    else params.set(key, next);
    pendingSearchParams.set(navigation, params);
    const query = params.toString();
    navigation.replace(`${navigation.pathname}${query ? `?${query}` : ""}${navigation.hash}`);
  }, [defaultValue, key, navigation]);
  return [value, setValue] as const;
}

/**
 * URL-backed state for reusable surfaces that can also render in isolation.
 * Tests and embedded editors keep local state until a navigation provider is
 * available.
 */
export function useOptionalSearchState(key: string, defaultValue = "") {
  const navigation = useOptionalNavigation();
  const [fallback, setFallback] = useState(defaultValue);
  const value = navigation?.searchParams.get(key) ?? fallback;
  const setValue = useCallback((next: string) => {
    if (!navigation) {
      setFallback(next || defaultValue);
      return;
    }
    const params = new URLSearchParams(latestSearchParams(navigation));
    if (!next || next === defaultValue) params.delete(key);
    else params.set(key, next);
    pendingSearchParams.set(navigation, params);
    const query = params.toString();
    navigation.replace(`${navigation.pathname}${query ? `?${query}` : ""}${navigation.hash}`);
  }, [defaultValue, key, navigation]);
  return [value, setValue] as const;
}
