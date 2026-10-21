import { Component, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { NavigationEnd, Router, RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { Title } from '@angular/platform-browser';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatTooltipModule } from '@angular/material/tooltip';
import { filter, firstValueFrom } from 'rxjs';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { AuthService, errorMessage } from '../../core/auth.service';
import { ApiError } from '../../core/api.types';
import * as check from '../../pages/shared/validation';
import { FormErrors } from '../../pages/shared/validation';
import { ThemeMode, ThemeService } from '../../core/theme.service';
import { LoadingService } from '../../core/loading.service';

type AgentOs = 'windows' | 'mac' | 'linux';

/** Guesses the visitor's operating system so that card is selected first. */
function detectOs(): AgentOs {
  const ua = navigator.userAgent.toLowerCase();
  if (ua.includes('win')) return 'windows';
  if (ua.includes('mac') || ua.includes('iphone') || ua.includes('ipad')) return 'mac';
  return 'linux';
}

interface NavChild {
  label: string;
  route: string;
}

interface NavItem {
  label: string;
  icon: string;
  route?: string;          // leaf item
  children?: NavChild[];   // expandable group
}

@Component({
  selector: 'app-shell',
  standalone: true,
  imports: [RouterOutlet, RouterLink, RouterLinkActive, MatIconModule, MatMenuModule, MatTooltipModule],
  templateUrl: './shell.component.html',
  styleUrl: './shell.component.scss',
})
export class ShellComponent {
  private readonly router = inject(Router);
  private readonly title = inject(Title);
  private readonly http = inject(HttpClient);
  readonly auth = inject(AuthService);
  readonly loading = inject(LoadingService);

  collapsed = signal(false);
  openGroup = signal<string | null>(null);

  readonly user = this.auth.user;
  readonly photoVersion = this.auth.photoVersion;

  // ---------- Settings ----------
  readonly theme = inject(ThemeService);
  settingsOpen = signal(false);
  readonly themes: { mode: ThemeMode; label: string; icon: string; hint: string }[] = [
    { mode: 'light', label: 'Light', icon: 'light_mode', hint: 'Bright and clear' },
    { mode: 'dark', label: 'Dark', icon: 'dark_mode', hint: 'Easy on the eyes' },
    { mode: 'system', label: 'System', icon: 'computer', hint: 'Match this computer' },
  ];

  // ---------- Download agent ----------
  agentOpen = signal(false);
  /** Two-step flow: pick the operating system, then who the install is for */
  agentStep = signal<'os' | 'mode'>('os');
  agentOs = signal<AgentOs>(detectOs());
  /** Which operating systems have an install package ready (filled when the dialog opens) */
  agentReady = signal<Record<AgentOs, boolean>>({ windows: false, mac: false, linux: false });
  readonly agentCards: { os: AgentOs; label: string }[] = [
    { os: 'windows', label: 'Windows' },
    { os: 'mac', label: 'Mac' },
    { os: 'linux', label: 'Linux' },
  ];

  /** True while the availability list is being fetched */
  agentLoading = signal(false);
  agentError = signal('');
  /** An operating system picked before the list arrived: move on as soon as it does */
  private agentPending: AgentOs | null = null;

  openAgent() {
    this.agentStep.set('os');
    this.agentOs.set(detectOs());
    this.agentReady.set({ windows: false, mac: false, linux: false });
    this.agentError.set('');
    this.agentPending = null;
    this.agentLoading.set(true);
    this.agentOpen.set(true);
    this.http.get<{ platforms: { os: AgentOs; available: boolean }[] }>('/api/agent/downloads')
      .subscribe({
        next: res => {
          const ready = { windows: false, mac: false, linux: false } as Record<AgentOs, boolean>;
          for (const p of res.platforms) ready[p.os] = p.available;
          this.agentReady.set(ready);
          this.agentLoading.set(false);
          if (this.agentPending && ready[this.agentPending]) this.agentStep.set('mode');
          this.agentPending = null;
        },
        error: err => {
          this.agentLoading.set(false);
          this.agentError.set(errorMessage(err, 'Could not check which agents are available.'));
        },
      });
  }

  /** Picks an operating system; moves on to the install choice when one is ready. */
  pickAgent(os: AgentOs) {
    this.agentOs.set(os);
    if (this.agentLoading()) { this.agentPending = os; return; }
    if (this.agentReady()[os]) this.agentStep.set('mode');
  }

  /** Back to the operating-system choice */
  backToOs() {
    this.agentStep.set('os');
  }

  agentDownloading = signal(false);

  /** Fetches the package and hands it to the browser, so an error never navigates away from the app */
  async downloadAgent(os: AgentOs) {
    if (this.agentDownloading()) return;
    this.agentDownloading.set(true);
    this.agentError.set('');
    try {
      const res = await firstValueFrom(this.http.get(`/api/agent/download?os=${os}`, { observe: 'response', responseType: 'blob' }));
      const name = /filename="?([^";]+)"?/.exec(res.headers.get('Content-Disposition') ?? '')?.[1]
        ?? { windows: 'hrlens-agent-windows.zip', mac: 'hrlens-agent-macos.zip', linux: 'hrlens-agent-linux.tar.gz' }[os];
      const url = URL.createObjectURL(res.body!);
      const a = document.createElement('a');
      a.href = url;
      a.download = name;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 60_000);
    } catch (err) {
      // A JSON error arrives as a blob; read it for the message
      let message = errorMessage(err, 'Could not download the agent.');
      if (err instanceof HttpErrorResponse && err.error instanceof Blob) {
        try { message = (JSON.parse(await err.error.text()) as ApiError).error || message; } catch { /* keep the fallback */ }
      }
      this.agentError.set(message);
    } finally {
      this.agentDownloading.set(false);
    }
  }

  cardLabel(os: AgentOs) {
    return this.agentCards.find(c => c.os === os)?.label ?? os;
  }

  // ---------- Change password ----------
  passwordOpen = signal(false);
  passwordDone = signal(false);
  pwSaving = signal(false);
  pwError = signal('');
  pw = { current: '', next: '', confirm: '' };
  /** Checks the password form as it is typed */
  readonly pwForm = new FormErrors(() => {
    const { current, next, confirm } = this.pw;
    return {
      currentPassword: current ? '' : 'Current password is required',
      newPassword: check.password(next, 'New password') ||
        (current && next === current ? 'Choose a password different from the current one' : ''),
      confirm: !confirm ? 'Confirm the new password' : confirm !== next ? 'The passwords do not match' : '',
    };
  });
  readonly pwErrors = this.pwForm.shown;

  /** Top bar heading: the route's title, and the menu group it sits in */
  pageTitle = signal('');
  section = signal<string | null>(null);

  private readonly allItems: NavItem[] = [
    {
      label: 'Time Tracker', icon: 'track_changes',
      children: [
        { label: 'Dashboard', route: '/dashboard' },
        { label: 'Attendance', route: '/attendance' },
        { label: 'Time Claim', route: '/time-claim' },
        { label: 'Heat Maps', route: '/heat-maps' },
      ],
    },
    {
      label: 'Productivity', icon: 'insert_chart_outlined',
      children: [
        { label: 'Trends', route: '/trends' },
        { label: 'Configure Apps', route: '/configure-apps' },
      ],
    },
    {
      label: 'Activity', icon: 'fact_check',
      children: [
        { label: 'Websites Visited', route: '/websites-visited' },
        { label: 'Applications Used', route: '/applications-used' },
        { label: 'By Categories', route: '/by-categories' },
        { label: 'Detailed Activity', route: '/detailed-activity' },
      ],
    },
    {
      label: 'Monitoring', icon: 'desktop_windows',
      children: [
        { label: 'Screenshots', route: '/screenshots' },
        { label: 'Screen Recordings', route: '/screen-recordings' },
        { label: 'Activity Levels', route: '/activity-levels' },
        { label: 'Live Screen', route: '/live-screen-monitoring' },
      ],
    },
    { label: 'Office TV', icon: 'tv', route: '/office-tv' },
    { label: 'Emails', icon: 'mail_outline', route: '/emails' },
    { label: 'Desktops', icon: 'computer', route: '/desktops' },
    {
      label: 'Administration', icon: 'settings',
      children: [
        { label: 'Users', route: '/users' },
        { label: 'Teams', route: '/teams' },
        { label: 'Shifts', route: '/shifts' },
      ],
    },
  ];

  /** Menu for the signed-in role: pages it cannot open are left out, and so are groups left empty */
  readonly navItems = computed(() => this.allItems
    .map(item => (item.children ? { ...item, children: item.children.filter(c => this.auth.can(c.route)) } : item))
    .filter(item => (item.children ? item.children.length : this.auth.can(item.route!))));

  constructor() {
    this.syncOpenGroup(this.router.url);
    this.router.events
      .pipe(filter((e): e is NavigationEnd => e instanceof NavigationEnd), takeUntilDestroyed())
      .subscribe(e => this.syncOpenGroup(e.urlAfterRedirects));
  }

  async signOut() {
    await this.auth.logout();
    this.router.navigateByUrl('/login');
  }

  openPassword() {
    this.pw = { current: '', next: '', confirm: '' };
    this.lastChecked = '';
    this.pwForm.reset();
    this.pwError.set('');
    this.passwordDone.set(false);
    this.passwordOpen.set(true);
  }

  closePassword() {
    this.pw = { current: '', next: '', confirm: '' };
    this.passwordOpen.set(false);
  }

  /** Stores a typed password and checks the form straight away */
  setPw(field: 'current' | 'next' | 'confirm', value: string) {
    this.pw[field] = value;
    this.pwError.set('');
    this.pwForm.touch({ current: 'currentPassword', next: 'newPassword', confirm: 'confirm' }[field]);
  }

  async savePassword() {
    if (this.pwSaving()) return;
    this.pwError.set('');
    if (!this.pwForm.submit()) {
      // Report a wrong current password together with the other mistakes (unless already shown)
      if (this.pw.current && !this.pwErrors()['currentPassword']) await this.checkCurrent();
      return;
    }

    this.pwSaving.set(true);
    try {
      await this.auth.changePassword(this.pw.current, this.pw.next);
      this.pw = { current: '', next: '', confirm: '' };
      this.passwordDone.set(true);
    } catch (err) {
      const fields = err instanceof HttpErrorResponse ? (err.error as ApiError | null)?.fields : undefined;
      if (fields) this.pwForm.setServer(fields);
      else this.pwError.set(errorMessage(err));
    } finally {
      this.pwSaving.set(false);
    }
  }

  /** The value last sent to the server, so leaving the field again does not count as another attempt */
  private lastChecked = '';

  /** Checks the current password with the server as soon as the field is left */
  async checkCurrent() {
    const current = this.pw.current;
    if (!current || current === this.lastChecked) return;
    this.lastChecked = current;
    try {
      await this.auth.verifyPassword(current);
    } catch (err) {
      if (this.pw.current !== current) return;   // typed again meanwhile
      const fields = err instanceof HttpErrorResponse ? (err.error as ApiError | null)?.fields : undefined;
      if (fields?.['currentPassword']) this.pwForm.setServerField('currentPassword', fields['currentPassword']);
      else this.pwError.set(errorMessage(err));
    }
  }

  toggle() {
    this.collapsed.update(v => !v);
  }

  toggleGroup(item: NavItem) {
    if (this.collapsed()) {
      this.collapsed.set(false);
      this.openGroup.set(item.label);
      return;
    }
    this.openGroup.update(g => (g === item.label ? null : item.label));
  }

  isGroupActive(item: NavItem) {
    const url = this.router.url.split('?')[0];
    return !!item.children?.some(c => url.startsWith(c.route));
  }

  private syncOpenGroup(url: string) {
    const path = url.split('?')[0];
    const group = this.allItems.find(i => i.children?.some(c => path.startsWith(c.route)));
    if (group) this.openGroup.set(group.label);
    this.section.set(group?.label ?? null);
    // The router sets the document title after NavigationEnd
    queueMicrotask(() => this.pageTitle.set(this.title.getTitle()));
  }
}
