import { computed, inject, Injectable, signal } from '@angular/core';
import { takeUntilDestroyed, toObservable } from '@angular/core/rxjs-interop';
import { HttpContextToken, HttpInterceptorFn } from '@angular/common/http';
import { NavigationCancel, NavigationEnd, NavigationError, NavigationStart, Router } from '@angular/router';
import { debounceTime, filter, finalize, first, merge, of, race, switchMap, timer } from 'rxjs';

/** Set on background requests (polling) so they do not show the progress bar */
export const SILENT = new HttpContextToken<boolean>(() => false);

/** How long the network must stay quiet before a newly opened page counts as loaded */
const SETTLE_MS = 200;
/** Show the page anyway after this long, so a slow request never leaves it blank */
const MAX_WAIT_MS = 15000;

const path = (url: string) => url.split(/[?#]/)[0];

/**
 * Drives the progress bar under the top bar: shown while any API request is in flight.
 * After moving to another page, `pageLoading` stays true (the shell hides the page) until
 * that page's first requests have finished.
 */
@Injectable({ providedIn: 'root' })
export class LoadingService {
  private readonly router = inject(Router);
  private readonly pending = signal(0);

  readonly pageLoading = signal(true);
  readonly busy = computed(() => this.pending() > 0 || this.pageLoading());

  constructor() {
    const pending$ = toObservable(this.pending);
    const settled = () => race(
      pending$.pipe(debounceTime(SETTLE_MS), first(n => n === 0)),
      timer(MAX_WAIT_MS),
    );

    // Query-string changes on the same page keep it on screen; only the bar shows
    this.router.events.pipe(
      filter((e): e is NavigationStart => e instanceof NavigationStart && path(e.url) !== path(this.router.url)),
      takeUntilDestroyed(),
    ).subscribe(() => this.pageLoading.set(true));

    merge(
      of(null),
      this.router.events.pipe(filter(e => e instanceof NavigationEnd || e instanceof NavigationCancel || e instanceof NavigationError)),
    ).pipe(switchMap(settled), takeUntilDestroyed())
      .subscribe(() => this.pageLoading.set(false));
  }

  /** Counts a request until it completes, fails or is cancelled */
  track() {
    this.pending.update(n => n + 1);
    return () => this.pending.update(n => n - 1);
  }
}

export const loadingInterceptor: HttpInterceptorFn = (req, next) => {
  if (req.context.get(SILENT)) return next(req);
  const done = inject(LoadingService).track();
  return next(req).pipe(finalize(done));
};
