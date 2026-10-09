import { get, post, setCsrfToken, setUnauthorizedHandler, ApiError } from "./api";
import type { Me, User } from "./types";

// Current session state shared by all pages.
export const session = $state<{ user: User | null; loaded: boolean }>({ user: null, loaded: false });

setUnauthorizedHandler(() => {
  session.user = null;
  setCsrfToken("");
});

function apply(me: Me) {
  session.user = me.user;
  setCsrfToken(me.csrf_token);
}

export async function loadSession() {
  try {
    apply(await get<Me>("/me"));
  } catch (e) {
    if (!(e instanceof ApiError && e.status === 401)) throw e;
    session.user = null;
  } finally {
    session.loaded = true;
  }
}

export async function login(username: string, password: string) {
  apply(await post<Me>("/auth/login", { username, password }));
}

export async function logout() {
  try {
    await post("/auth/logout");
  } finally {
    session.user = null;
    setCsrfToken("");
  }
}

export async function refreshSession() {
  apply(await get<Me>("/me"));
}

export const isAdmin = () => session.user?.role === "admin";
