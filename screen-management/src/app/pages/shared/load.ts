import { Signal } from '@angular/core';
import { toObservable, toSignal } from '@angular/core/rxjs-interop';
import { catchError, concat, debounceTime, map, Observable, of, switchMap } from 'rxjs';
import { errorMessage } from '../../core/auth.service';

/** A request's progress: data, loading or a message */
export interface Loaded<T> { loading: boolean; data: T | null; error: string; }

/**
 * Fetches again whenever request changes (cancelling the previous call), keeping the last
 * result on screen while loading. Call it in a field initializer (it needs an injection context).
 */
export function loadOnChange<R, T>(request: Signal<R>, fetch: (r: R) => Observable<T>): Signal<Loaded<T>> {
  let last: T | null = null;
  return toSignal(toObservable(request).pipe(
    debounceTime(150),
    switchMap(r => concat(
      of<Loaded<T>>({ loading: true, data: last, error: '' }),
      fetch(r).pipe(
        map(data => { last = data; return { loading: false, data, error: '' }; }),
        catchError(err => of<Loaded<T>>({ loading: false, data: null, error: errorMessage(err) })),
      ),
    )),
  ), { initialValue: { loading: true, data: null, error: '' } });
}
