import { computed, inject, Injectable, signal } from '@angular/core';
import { HttpClient, HttpErrorResponse, HttpInterceptorFn } from '@angular/common/http';
import { CanActivateChildFn, CanActivateFn, Router } from '@angular/router';
import { catchError, firstValueFrom, throwError } from 'rxjs';
import { TimeTrackerService } from '../pages/dashboard/time-tracker.service';
import { ApiError, ApiUser, Role } from './api.types';

export type { Role } from './api.types';

export const ROLES: { value: Role; label: string }[] = [
  { value: 'super_admin', label: 'Super Admin' },
  { value: 'manager', label: 'Manager' },
  { value: 'user', label: 'User' },
];

const EVERYONE: Role[] = ['super_admin', 'manager', 'user'];
const MANAGERS: Role[] = ['super_admin', 'manager'];
const SUPER_ADMIN: Role[] = ['super_admin'];

/**
 * Who can open each page (by route path). Drives the side menu and the route guard.
 * This only hides pages: the server checks the role again on every request.
 */
export const ACCESS: Record<string, Role[]> = {
  'dashboard': EVERYONE,
  'attendance': EVERYONE,
  'time-claim': EVERYONE,
  'heat-maps': EVERYONE,
  'trends': EVERYONE,
  'configure-apps': SUPER_ADMIN,
  'websites-visited': EVERYONE,      // a user sees only their own activity
  'applications-used': EVERYONE,
  'by-categories': EVERYONE,
  'detailed-activity': EVERYONE,
  'screenshots': EVERYONE,          // a user sees only their own
  'screen-recordings': MANAGERS,
  'activity-levels': MANAGERS,
  'live-screen-monitoring': MANAGERS,
  'teams': MANAGERS,
  'shifts': SUPER_ADMIN,
  'office-tv': SUPER_ADMIN,
  'emails': SUPER_ADMIN,
  'desktops': SUPER_ADMIN,
  'users': SUPER_ADMIN,
  'profile': EVERYONE,
  'administration': SUPER_ADMIN,
};

/** Message to show for a failed request */
export function errorMessage(err: unknown, fallback = 'Something went wrong. Please try again.') {
  if (err instanceof HttpErrorResponse) {
    if (err.status === 0 || err.status >= 502) return 'Cannot reach the server. Check that the backend is running.';
    return (err.error as ApiError | null)?.error ?? fallback;
  }
  return fallback;
}

@Injectable({ providedIn: 'root' })
export class AuthService {
  private readonly http = inject(HttpClient);
  private readonly people = inject(TimeTrackerService);

  readonly user = signal<ApiUser | null>(null);
  readonly role = computed<Role>(() => this.user()?.role ?? 'user');
  readonly roleLabel = computed(() => ROLES.find(r => r.value === this.role())!.label);
  readonly initials = computed(() =>
    (this.user()?.name ?? '').split(/\s+/).filter(Boolean).slice(0, 2).map(p => p[0].toUpperCase()).join(''));

  private restored?: Promise<void>;

  /** Restores the session from the cookie, once per page load */
  restore() {
    return this.restored ??= firstValueFrom(this.http.get<ApiUser>('/api/auth/me'))
      .then(u => this.user.set(u), () => this.user.set(null));
  }

  /** Bumped when the photo changes so <img> tags fetch it again */
  readonly photoVersion = signal(Date.now());

  /** Reads the signed-in user again, e.g. after their photo changed */
  async refresh() {
    this.user.set(await firstValueFrom(this.http.get<ApiUser>('/api/auth/me')));
    this.photoVersion.set(Date.now());
  }

  async login(email: string, password: string) {
    this.people.clear();
    this.user.set(await firstValueFrom(this.http.post<ApiUser>('/api/auth/login', { email, password })));
    this.restored = Promise.resolve();
  }

  async logout() {
    await firstValueFrom(this.http.post('/api/auth/logout', null)).catch(() => undefined);
    this.forget();
  }

  /** Changes the signed-in person's password; the server signs out their other devices */
  changePassword(currentPassword: string, newPassword: string) {
    return firstValueFrom(this.http.post<void>('/api/auth/password', { currentPassword, newPassword }));
  }

  /** Resolves when currentPassword is right; fails with a field error when it is not */
  verifyPassword(currentPassword: string) {
    return firstValueFrom(this.http.post<void>('/api/auth/password/verify', { currentPassword }));
  }

  /** Drops the local session, e.g. after the server says it has expired */
  forget() {
    this.user.set(null);
    this.people.clear();
  }

  can(path: string) {
    const allowed = ACCESS[path.replace(/^\//, '')];
    return !allowed || allowed.includes(this.role());
  }
}

// ---------- Guards ----------

/** Signed-in pages: restores the session and loads the people list before the page opens */
export const signedInGuard: CanActivateFn = async (_route, state) => {
  const auth = inject(AuthService), router = inject(Router), people = inject(TimeTrackerService);
  await auth.restore();
  const user = auth.user();
  if (!user) return router.createUrlTree(['/login'], { queryParams: state.url === '/' ? {} : { returnUrl: state.url } });
  try {
    await people.loadPeople(user.id);
  } catch (err) {
    // The interceptor already handles 401. Any other failure (server down for a moment) is not a
    // reason to drop a valid session: the page opens and shows the error itself.
    if (err instanceof HttpErrorResponse && err.status === 403) {
      auth.forget();
      return router.createUrlTree(['/login']);
    }
  }
  return true;
};

export const signedOutGuard: CanActivateFn = async () => {
  const auth = inject(AuthService), router = inject(Router);
  await auth.restore();
  return auth.user() ? router.parseUrl('/dashboard') : true;
};

export const roleGuard: CanActivateChildFn = async child => {
  const auth = inject(AuthService), router = inject(Router);
  await auth.restore();
  return auth.can(child.routeConfig?.path ?? '') || router.parseUrl('/dashboard');
};

// ---------- HTTP ----------

/** Sends the session cookie, and returns to the sign-in page when the session has ended */
export const sessionInterceptor: HttpInterceptorFn = (req, next) => {
  const auth = inject(AuthService), router = inject(Router);
  return next(req.clone({ withCredentials: true })).pipe(
    catchError((err: HttpErrorResponse) => {
      if (err.status === 401 && !req.url.includes('/api/auth/') && auth.user()) {
        auth.forget();
        router.navigate(['/login'], { queryParams: { returnUrl: router.url } });
      }
      return throwError(() => err);
    }),
  );
};
