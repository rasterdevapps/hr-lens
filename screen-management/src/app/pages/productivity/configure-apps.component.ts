import { Component, computed, inject, signal } from '@angular/core';
import { DecimalPipe } from '@angular/common';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { firstValueFrom } from 'rxjs';
import { Productivity } from '../../core/activity.service';
import { ApiError } from '../../core/api.types';
import { AuthService, errorMessage } from '../../core/auth.service';
import { ProductivityService, Rule, RuleKind, RuleSort } from '../../core/productivity.service';
import { hash } from '../dashboard/time-tracker.service';
import { loadOnChange } from '../shared/load';
import { cleanDomain, Errors, FormErrors, requiredText, website } from '../shared/validation';
import { LEVELS, PRODUCTIVITY_TABS } from './trends.component';

const PAGE_SIZE = 50;
const TILE_COLORS = ['#4f46e5', '#0ea5e9', '#f97316', '#14b8a6', '#a855f7', '#ec4899', '#22c55e', '#64748b'];

interface Draft { kind: RuleKind; name: string; category: string; productivity: Productivity; }

/** Productivity > Configure Apps: how each application and website is rated */
@Component({
  selector: 'app-configure-apps',
  standalone: true,
  imports: [DecimalPipe, RouterLink, MatIconModule, MatTooltipModule],
  templateUrl: './configure-apps.component.html',
  // Tabs and tables come from the Time Tracker page; search, filters and list card from Activity
  styleUrls: ['../dashboard/dashboard.component.scss', '../activity/activity.component.scss', './configure-apps.component.scss'],
})
export class ConfigureAppsComponent {
  private readonly api = inject(ProductivityService);
  private readonly auth = inject(AuthService);

  readonly tabs = PRODUCTIVITY_TABS.filter(t => this.auth.can(t.route));
  readonly levels = LEVELS;
  readonly pageSize = PAGE_SIZE;
  readonly sorts: { value: RuleSort; label: string }[] = [
    { value: 'time', label: 'Most time' },
    { value: 'hits', label: 'Most hits' },
    { value: 'name', label: 'Name' },
  ];

  // ---------- Filters ----------
  kind = signal<RuleKind>('application');
  search = signal('');
  productivity = signal<Productivity | ''>('');
  category = signal('');
  sort = signal<RuleSort>('time');
  page = signal(1);
  private attempt = signal(0);

  private readonly request = computed(() => ({
    kind: this.kind(), search: this.search().trim(), productivity: this.productivity(), category: this.category(),
    sort: this.sort(), page: this.page(), pageSize: PAGE_SIZE, attempt: this.attempt(),
  }));

  readonly list = loadOnChange(this.request, r => this.api.rules(r));

  /** Changes saved here but not yet in a fresh list from the server */
  private readonly saved = signal<ReadonlyMap<number, Partial<Rule>>>(new Map());

  readonly items = computed(() => (this.list().data?.items ?? []).map(r => ({ ...r, ...this.saved().get(r.id) })));
  readonly categories = computed(() => this.list().data?.categories ?? []);

  pageRange = computed(() => {
    const total = this.list().data?.total ?? 0;
    const start = (this.page() - 1) * PAGE_SIZE;
    return { first: total ? start + 1 : 0, last: Math.min(start + PAGE_SIZE, total), total };
  });

  /** Changing a filter starts again from the first page */
  setFilter<T>(target: { set(v: T): void }, value: T) {
    target.set(value);
    this.page.set(1);
    this.clearSelection();
  }

  retry() { this.attempt.update(n => n + 1); }

  // ---------- Selection ----------
  selected = signal<ReadonlySet<number>>(new Set());
  allSelected = computed(() => this.items().length > 0 && this.items().every(r => this.selected().has(r.id)));

  toggle(id: number) {
    this.selected.update(s => {
      const next = new Set(s);
      if (!next.delete(id)) next.add(id);
      return next;
    });
  }

  clearSelection() { this.selected.set(new Set()); }

  toggleAll() {
    this.selected.set(this.allSelected() ? new Set() : new Set(this.items().map(r => r.id)));
  }

  // ---------- Saving ----------
  busy = signal(false);
  notice = signal<{ text: string; error: boolean } | null>(null);
  private noticeTimer?: ReturnType<typeof setTimeout>;

  private say(text: string, error = false) {
    this.notice.set({ text, error });
    clearTimeout(this.noticeTimer);
    this.noticeTimer = setTimeout(() => this.notice.set(null), error ? 6000 : 3000);
  }

  private remember(ids: number[], change: Partial<Rule>) {
    this.saved.update(m => {
      const next = new Map(m);
      for (const id of ids) next.set(id, { ...next.get(id), ...change });
      return next;
    });
  }

  async rate(rule: Rule, productivity: Productivity) {
    if (rule.productivity === productivity) return;
    await this.save([rule.id], { productivity }, `${rule.name} is now ${productivity}`,
      () => this.api.updateRule(rule.id, { category: rule.category, productivity }));
  }

  async recategorize(rule: Rule, category: string) {
    if (rule.category === category) return;
    await this.save([rule.id], { category }, `${rule.name} moved to ${category}`,
      () => this.api.updateRule(rule.id, { category, productivity: rule.productivity }));
  }

  async rateSelected(productivity: Productivity) {
    const ids = [...this.selected()];
    await this.save(ids, { productivity }, `${ids.length} rated ${productivity}`, () => this.api.updateRules(ids, productivity));
    this.clearSelection();
  }

  /** Shows the change at once, saves it, and reloads the list (hits and filters may change) */
  private async save(ids: number[], change: Partial<Rule>, done: string, call: () => ReturnType<ProductivityService['updateRules']>) {
    const before = new Map(this.saved());
    this.remember(ids, change);
    this.busy.set(true);
    try {
      await firstValueFrom(call());
      this.say(done + ' · past activity re-rated');
      this.retry();
    } catch (err) {
      this.saved.set(before);
      this.say(errorMessage(err), true);
    } finally {
      this.busy.set(false);
    }
  }

  // ---------- Add rule ----------
  draft = signal<Draft | null>(null);
  /** Checks the new rule as it is typed */
  readonly form = new FormErrors(() => this.checkDraft());
  private readonly formError = signal('');
  readonly draftErrors = computed(() => (this.formError() ? { ...this.form.shown(), form: this.formError() } : this.form.shown()));

  private checkDraft(): Errors {
    const d = this.draft();
    if (!d) return {};
    const category = d.category.trim();
    return {
      name: d.kind === 'website' ? website(d.name) : requiredText(d.name, 120, 'Application name'),
      category: category && new TextEncoder().encode(category).length > 60 ? 'Use at most 60 characters' : '',
    };
  }

  openAdd() {
    this.form.reset();
    this.formError.set('');
    this.draft.set({ kind: this.kind(), name: '', category: 'Uncategorized', productivity: 'productive' });
  }

  patch(change: Partial<Draft>) {
    this.draft.update(d => (d ? { ...d, ...change } : d));
    this.formError.set('');
    if ('name' in change) this.form.touch('name');
    else if ('category' in change) this.form.touch('category');
    else this.form.refresh();   // kind changed: the name is checked differently
  }

  async addRule() {
    const d = this.draft();
    if (!d || this.busy()) return;
    this.formError.set('');
    if (!this.form.submit()) return;
    this.busy.set(true);
    try {
      await firstValueFrom(this.api.createRule(d));
      this.draft.set(null);
      this.say(`Rule added for ${d.kind === 'website' ? cleanDomain(d.name) : d.name.trim()}`);
      if (this.kind() !== d.kind) this.setFilter(this.kind, d.kind);
      this.retry();
    } catch (err) {
      const fields = err instanceof HttpErrorResponse ? (err.error as ApiError | null)?.fields : undefined;
      if (fields) this.form.setServer(fields);
      else this.formError.set(errorMessage(err));
    } finally {
      this.busy.set(false);
    }
  }

  // ---------- Display ----------
  duration(seconds: number) {
    const h = Math.floor(seconds / 3600), m = Math.floor((seconds % 3600) / 60);
    return h ? `${h}h ${String(m).padStart(2, '0')}m` : m ? `${m}m` : seconds ? `${seconds}s` : '–';
  }

  tile(name: string) {
    return { letter: (name[0] ?? '?').toUpperCase(), color: TILE_COLORS[Math.abs(hash(name)) % TILE_COLORS.length] };
  }

  /** The categories list plus the rule's own, if it is not there */
  options(rule: Rule) {
    const list = this.categories();
    return list.includes(rule.category) ? list : [rule.category, ...list];
  }
}
