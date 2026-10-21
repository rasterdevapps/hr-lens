import { computed, DestroyRef, effect, inject, Injectable, signal } from '@angular/core';
import { DOCUMENT } from '@angular/common';

export type ThemeMode = 'light' | 'dark' | 'system';

// Read by the inline script in index.html too, so the first paint already has the right colours
const STORAGE_KEY = 'hrlens-theme';

/** Light, dark or follow the computer; applied as <html data-theme="light|dark"> and remembered in this browser */
@Injectable({ providedIn: 'root' })
export class ThemeService {
  private readonly document = inject(DOCUMENT);
  private readonly media = this.document.defaultView?.matchMedia('(prefers-color-scheme: dark)');

  readonly mode = signal<ThemeMode>(this.saved());
  private readonly systemDark = signal(this.media?.matches ?? false);

  /** The theme actually showing */
  readonly dark = computed(() => this.mode() === 'dark' || (this.mode() === 'system' && this.systemDark()));

  constructor() {
    const onChange = (e: MediaQueryListEvent) => this.systemDark.set(e.matches);
    this.media?.addEventListener('change', onChange);
    inject(DestroyRef).onDestroy(() => this.media?.removeEventListener('change', onChange));

    effect(() => {
      this.document.documentElement.dataset['theme'] = this.dark() ? 'dark' : 'light';
    });
  }

  set(mode: ThemeMode) {
    this.mode.set(mode);
    try {
      localStorage.setItem(STORAGE_KEY, mode);
    } catch {
      // Private windows may refuse storage: the choice then lasts until the page is closed
    }
  }

  private saved(): ThemeMode {
    try {
      const v = localStorage.getItem(STORAGE_KEY);
      return v === 'light' || v === 'dark' || v === 'system' ? v : 'light';
    } catch {
      return 'light';
    }
  }
}
