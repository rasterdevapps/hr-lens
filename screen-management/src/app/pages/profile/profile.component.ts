import { Component, computed, HostListener, inject, signal } from '@angular/core';
import { DatePipe } from '@angular/common';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { firstValueFrom } from 'rxjs';
import { ApiError, DocumentFolder, Profile, ProfileDetails, UserDocument } from '../../core/api.types';
import { AuthService, errorMessage } from '../../core/auth.service';
import { FormErrors } from '../shared/validation';

type Tab = 'about' | 'profile' | 'job' | 'documents';
type Section = 'primary' | 'contact' | 'address' | 'job' | 'org';

/** One value on a card, and the input that edits it */
export interface Field {
  key: string;
  label: string;
  type?: 'text' | 'date' | 'select' | 'textarea' | 'number' | 'bool' | 'email' | 'tel';
  options?: { value: string; label: string }[];
  required?: boolean;
  max?: number;
  /** Shown but set by an administrator on the Users page */
  readonly?: boolean;
}

interface Relation { name: string; relation: string; phone: string; }

interface TimelineEvent { at: string; icon: string; title: string; detail: string; }

const yesNo = [{ value: 'true', label: 'Yes' }, { value: 'false', label: 'No' }];
const opt = (values: [string, string][]) => values.map(([value, label]) => ({ value, label }));

export const PRIMARY_FIELDS: Field[] = [
  { key: 'firstName', label: 'First Name', readonly: true },
  { key: 'lastName', label: 'Last Name', readonly: true },
  { key: 'displayName', label: 'Display Name', max: 100 },
  { key: 'gender', label: 'Gender', type: 'select', options: opt([['male', 'Male'], ['female', 'Female'], ['other', 'Other']]) },
  { key: 'dateOfBirth', label: 'Date Of Birth', type: 'date' },
  { key: 'maritalStatus', label: 'Marital Status', type: 'select', options: opt([['single', 'Single'], ['married', 'Married'], ['divorced', 'Divorced'], ['widowed', 'Widowed']]) },
  { key: 'pwd', label: 'PWD', type: 'bool', options: yesNo },
  { key: 'bloodGroup', label: 'Blood Group', type: 'select', options: ['A+', 'A-', 'B+', 'B-', 'AB+', 'AB-', 'O+', 'O-'].map(v => ({ value: v, label: v })) },
];

export const CONTACT_FIELDS: Field[] = [
  { key: 'workMail', label: 'Work Mail', type: 'email' },
  { key: 'personalMail', label: 'Personal Mail', type: 'email' },
  { key: 'mobilePhone', label: 'Mobile Phone', type: 'tel' },
  { key: 'workPhone', label: 'Work Phone', type: 'tel' },
  { key: 'residencePhone', label: 'Residence Phone', type: 'tel' },
  { key: 'skype', label: 'Skype', max: 100 },
];

export const ADDRESS_FIELDS: Field[] = [
  { key: 'current', label: 'Current Address', type: 'textarea', max: 500 },
  { key: 'permanent', label: 'Permanent Address', type: 'textarea', max: 500 },
];

export const JOB_FIELDS: Field[] = [
  { key: 'employeeCode', label: 'Employee Id', readonly: true },
  { key: 'dateOfJoining', label: 'Date Of Joining', type: 'date' },
  { key: 'inProbation', label: 'In Probation?', type: 'bool', options: yesNo },
  { key: 'jobTitle', label: 'Job Title', max: 100 },
  { key: 'jobCategory', label: 'Job Category', type: 'select', options: opt([['professionals', 'Professionals'], ['management', 'Management'], ['technical', 'Technical'], ['support', 'Support'], ['other', 'Other']]) },
  { key: 'noticeMonths', label: 'Notice Period (months)', type: 'number' },
  { key: 'workType', label: 'Work Type', type: 'select', options: opt([['permanent', 'Permanent'], ['contract', 'Contract'], ['intern', 'Intern'], ['consultant', 'Consultant']]) },
  { key: 'employmentType', label: 'Employment Type', type: 'select', options: opt([['full_time', 'Full-Time Employee'], ['part_time', 'Part-Time Employee'], ['intern', 'Intern'], ['contractor', 'Contractor']]) },
];

export const ORG_FIELDS: Field[] = [
  { key: 'department', label: 'Department', readonly: true },
  { key: 'practice', label: 'Practice', max: 100 },
  { key: 'location', label: 'Location', readonly: true },
  { key: 'legalEntity', label: 'Legal Entity', max: 100 },
  { key: 'team', label: 'Team', readonly: true },
  { key: 'manager', label: 'Reports To', readonly: true },
];

/** Summary on the About tab: a few of the primary details */
export const SUMMARY_FIELDS = PRIMARY_FIELDS.filter(f => ['firstName', 'lastName', 'maritalStatus', 'gender', 'dateOfBirth', 'pwd'].includes(f.key));

export const FOLDERS: { key: DocumentFolder; label: string; icon: string; fields: Field[] }[] = [
  {
    key: 'degree', label: 'Degree & Certification', icon: 'school',
    fields: [
      { key: 'degree', label: 'Degree', type: 'select', required: true, options: opt([['SSLC', 'SSLC'], ['HSC', 'HSC'], ['Diploma', 'Diploma'], ['B.E / B.Tech', 'B.E / B.Tech'], ['B.Sc', 'B.Sc'], ['B.Com', 'B.Com'], ['BCA', 'BCA'], ['BA', 'BA'], ['M.E / M.Tech', 'M.E / M.Tech'], ['M.Sc', 'M.Sc'], ['MCA', 'MCA'], ['MBA', 'MBA'], ['MA', 'MA'], ['Ph.D', 'Ph.D'], ['Certification', 'Certification'], ['Other', 'Other']]) },
      { key: 'branch', label: 'Branch/Specialization', required: true, max: 100 },
      { key: 'yearOfJoining', label: 'Year Of Joining', type: 'date', required: true },
      { key: 'yearOfCompletion', label: 'Year Of Completion', type: 'date', required: true },
      { key: 'cgpa', label: 'CGPA/Percentage', required: true, max: 20 },
      { key: 'university', label: 'University/College', required: true, max: 200 },
    ],
  },
  {
    key: 'experience', label: 'Previous Experience', icon: 'work',
    fields: [
      { key: 'company', label: 'Company', required: true, max: 200 },
      { key: 'designation', label: 'Designation', required: true, max: 100 },
      { key: 'from', label: 'From', type: 'date', required: true },
      { key: 'to', label: 'To', type: 'date' },
      { key: 'location', label: 'Location', max: 100 },
      { key: 'description', label: 'Description', type: 'textarea', max: 1000 },
    ],
  },
  {
    key: 'identity', label: 'Identity Docs', icon: 'folder',
    fields: [
      { key: 'documentType', label: 'Document Type', type: 'select', required: true, options: opt([['Aadhaar', 'Aadhaar'], ['PAN', 'PAN'], ['Passport', 'Passport'], ['Driving Licence', 'Driving Licence'], ['Voter ID', 'Voter ID'], ['Other', 'Other']]) },
      { key: 'number', label: 'Document Number', required: true, max: 50 },
      { key: 'issuedOn', label: 'Issued On', type: 'date' },
      { key: 'expiresOn', label: 'Expires On', type: 'date' },
      { key: 'issuedBy', label: 'Issued By', max: 100 },
    ],
  },
];

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const PHONE = /^\+?[0-9][0-9 -]{5,19}$/;
const bytes = (s: string) => new TextEncoder().encode(s).length;

/** Checks one field the way the server does */
function checkField(f: Field, value: string): string {
  const v = value.trim();
  if (!v) return f.required ? `${f.label} is required` : '';
  if (f.max && bytes(v) > f.max) return `Use at most ${f.max} characters`;
  switch (f.type) {
    case 'email': return EMAIL.test(v) ? '' : 'Enter a valid email address';
    case 'tel': return PHONE.test(v) ? '' : 'Enter a phone number with 6 to 20 digits';
    case 'date': return /^\d{4}-\d{2}-\d{2}$/.test(v) && !isNaN(Date.parse(v)) ? '' : 'Enter a valid date';
    case 'number': return /^\d+$/.test(v) && +v <= 12 ? '' : 'Enter 0 to 12 months';
  }
  return '';
}

@Component({
  selector: 'app-profile',
  standalone: true,
  imports: [DatePipe, MatIconModule, MatTooltipModule],
  templateUrl: './profile.component.html',
  styleUrl: './profile.component.scss',
})
export class ProfileComponent {
  private readonly http = inject(HttpClient);
  readonly auth = inject(AuthService);

  readonly fields = { primary: PRIMARY_FIELDS, contact: CONTACT_FIELDS, address: ADDRESS_FIELDS, job: JOB_FIELDS, org: ORG_FIELDS };
  readonly summaryFields = SUMMARY_FIELDS;
  readonly folders = FOLDERS;
  readonly tabs: { key: Tab; label: string }[] = [
    { key: 'about', label: 'About' }, { key: 'profile', label: 'Profile' }, { key: 'job', label: 'Job' }, { key: 'documents', label: 'Documents' },
  ];

  profile = signal<Profile | null>(null);
  loadError = signal('');
  notice = signal<{ text: string; bad: boolean } | null>(null);
  private noticeTimer?: ReturnType<typeof setTimeout>;

  /** Shows a short message at the bottom of the page; errors stay a little longer */
  toast(text: string, bad = false) {
    clearTimeout(this.noticeTimer);
    this.notice.set({ text, bad });
    this.noticeTimer = setTimeout(() => this.notice.set(null), bad ? 6000 : 3000);
  }
  tab = signal<Tab>('about');
  aboutView = signal<'summary' | 'timeline'>('summary');
  folder = signal<DocumentFolder>('degree');
  copied = signal(false);

  readonly user = computed(() => this.profile()?.user ?? this.auth.user());
  readonly details = computed(() => this.profile()?.details ?? null);
  readonly photoUrl = computed(() => (this.profile()?.hasPhoto ? `/api/profile/photo?v=${this.auth.photoVersion()}` : ''));
  readonly displayName = computed(() => this.details()?.primary.displayName || this.user()?.name || '');
  readonly folderInfo = computed(() => FOLDERS.find(f => f.key === this.folder())!);
  readonly folderDocs = computed(() => (this.profile()?.documents ?? []).filter(d => d.folder === this.folder()));
  readonly docCounts = computed(() => {
    const counts: Record<string, number> = { degree: 0, experience: 0, identity: 0 };
    for (const d of this.profile()?.documents ?? []) counts[d.folder]++;
    return counts;
  });

  /** Events in date order, newest first */
  readonly timeline = computed<TimelineEvent[]>(() => {
    const p = this.profile();
    if (!p) return [];
    const events: TimelineEvent[] = [{ at: p.joinedAt, icon: 'person_add', title: 'Account created', detail: 'Added to HR Lens' }];
    if (p.details.job.dateOfJoining) {
      events.push({ at: p.details.job.dateOfJoining, icon: 'badge', title: 'Joined the organisation', detail: p.details.job.jobTitle || 'Date of joining' });
    }
    if (p.updatedAt) events.push({ at: p.updatedAt, icon: 'edit', title: 'Profile updated', detail: 'Details last changed' });
    for (const d of p.documents) {
      const f = FOLDERS.find(x => x.key === d.folder)!;
      events.push({ at: d.createdAt, icon: f.icon, title: `Added to ${f.label}`, detail: this.docTitle(d) });
    }
    return events.sort((a, b) => Date.parse(b.at) - Date.parse(a.at));
  });

  constructor() {
    this.load();
  }

  async load() {
    try {
      this.profile.set(await firstValueFrom(this.http.get<Profile>('/api/profile')));
      this.loadError.set('');
    } catch (err) {
      this.loadError.set(errorMessage(err, 'Could not load your profile.'));
    }
  }

  // ---------- Showing values ----------

  /** The value a card shows for a field, or '' when not set */
  value(section: Section | 'summary', f: Field): string {
    const p = this.profile();
    if (!p) return '';
    const u = p.user, d = p.details;
    switch (f.key) {
      case 'firstName': return u.firstName;
      case 'lastName': return u.lastName;
      case 'employeeCode': return u.employeeCode;
      case 'department': return u.department?.name ?? '';
      case 'location': return u.branch?.name ?? '';
      case 'team': return u.team?.name ?? '';
      case 'manager': return u.manager?.name ?? '';
    }
    const group = section === 'summary' ? 'primary' : section === 'org' ? 'job' : section;
    const raw = (d[group] as unknown as Record<string, unknown>)[f.key];
    return this.format(f, raw);
  }

  format(f: Field, raw: unknown): string {
    if (raw === null || raw === undefined || raw === '') return '';
    if (f.type === 'bool') return raw ? 'Yes' : 'No';
    if (f.type === 'select') return f.options?.find(o => o.value === String(raw))?.label ?? String(raw);
    if (f.type === 'date') {
      const t = Date.parse(String(raw));
      return isNaN(t) ? String(raw) : new Date(t).toLocaleDateString('en-GB', { day: '2-digit', month: 'short', year: 'numeric' });
    }
    return String(raw);
  }

  docTitle(d: UserDocument) {
    const v = d.details;
    switch (d.folder) {
      case 'degree': return [v['degree'], v['branch']].filter(Boolean).join(' · ');
      case 'experience': return [v['designation'], v['company']].filter(Boolean).join(' at ');
      default: return [v['documentType'], v['number']].filter(Boolean).join(' · ');
    }
  }

  docFields(d: UserDocument) {
    return FOLDERS.find(f => f.key === d.folder)!.fields;
  }

  fileSize(n: number | null) {
    if (n === null) return '';
    return n < 1024 * 1024 ? `${Math.max(1, Math.round(n / 1024))} KB` : `${(n / 1024 / 1024).toFixed(1)} MB`;
  }

  async copyEmail() {
    const email = this.user()?.email;
    if (!email) return;
    try {
      await navigator.clipboard.writeText(email);
      this.copied.set(true);
      setTimeout(() => this.copied.set(false), 1500);
    } catch { /* clipboard blocked: nothing to do */ }
  }

  // ---------- Photo ----------
  photoBusy = signal(false);

  async uploadPhoto(input: HTMLInputElement) {
    const file = input.files?.[0];
    input.value = '';
    if (!file || this.photoBusy()) return;
    const body = new FormData();
    body.append('file', file, file.name);
    this.photoBusy.set(true);
    try {
      await firstValueFrom(this.http.post('/api/profile/photo', body));
      await Promise.all([this.load(), this.auth.refresh()]);
      this.toast('Photo updated');
    } catch (err) {
      this.toast(errorMessage(err, 'Could not upload the photo.'), true);
    } finally {
      this.photoBusy.set(false);
    }
  }

  async removePhoto() {
    if (this.photoBusy()) return;
    this.photoBusy.set(true);
    try {
      await firstValueFrom(this.http.delete('/api/profile/photo'));
      await Promise.all([this.load(), this.auth.refresh()]);
      this.toast('Photo removed');
    } catch (err) {
      this.toast(errorMessage(err, 'Could not remove the photo.'), true);
    } finally {
      this.photoBusy.set(false);
    }
  }

  // ---------- Editing a section ----------
  editing = signal<Section | null>(null);
  draft: Record<string, string> = {};
  saving = signal(false);
  saveError = signal('');
  readonly editFields = computed(() => (this.editing() ? this.fields[this.editing()!].filter(f => !f.readonly) : []));
  readonly editTitle = computed(() => ({
    primary: 'Primary Details', contact: 'Contact Details', address: 'Address', job: 'Job Details', org: 'Organization',
  })[this.editing() ?? 'primary']);
  readonly form = new FormErrors(() => {
    const errs: Record<string, string> = {};
    for (const f of this.editFields()) errs[f.key] = checkField(f, this.draft[f.key] ?? '');
    return errs;
  });
  readonly errors = this.form.shown;

  edit(section: Section) {
    const d = this.details();
    if (!d) return;
    const group = section === 'org' ? d.job : d[section];
    this.draft = {};
    for (const f of this.fields[section]) {
      if (f.readonly) continue;
      const raw = (group as unknown as Record<string, unknown>)[f.key];
      this.draft[f.key] = raw === null || raw === undefined ? '' : String(raw);
    }
    this.form.reset();
    this.saveError.set('');
    this.editing.set(section);
  }

  set(key: string, value: string) {
    this.draft[key] = value;
    this.saveError.set('');
    this.form.touch(key);
  }

  cancelEdit() {
    this.editing.set(null);
  }

  async saveSection() {
    const section = this.editing(), d = this.details();
    if (!section || !d || this.saving()) return;
    if (!this.form.submit()) return;
    const next: ProfileDetails = structuredClone(d);
    const group = (section === 'org' ? next.job : next[section]) as unknown as Record<string, unknown>;
    for (const f of this.editFields()) {
      const v = (this.draft[f.key] ?? '').trim();
      group[f.key] = f.type === 'bool' ? (v === '' ? null : v === 'true')
        : f.type === 'number' ? (v === '' ? null : Number(v))
        : v;
    }
    const prefix = section === 'org' ? 'job' : section;
    const title = this.editTitle();
    if (await this.putDetails(next, prefix)) {
      this.editing.set(null);
      this.toast(`${title} saved`);
    }
  }

  /** Saves the whole details document; field errors come back as "section.field" */
  private async putDetails(next: ProfileDetails, prefix: string): Promise<boolean> {
    this.saving.set(true);
    this.saveError.set('');
    try {
      const saved = await firstValueFrom(this.http.put<ProfileDetails>('/api/profile', next));
      this.profile.update(p => (p ? { ...p, details: saved, updatedAt: new Date().toISOString() } : p));
      return true;
    } catch (err) {
      const fields = err instanceof HttpErrorResponse ? (err.error as ApiError | null)?.fields : undefined;
      if (fields) {
        const local: Record<string, string> = {};
        for (const [k, v] of Object.entries(fields)) local[k.startsWith(prefix + '.') ? k.slice(prefix.length + 1) : k] = v;
        this.form.setServer(local);
        this.relForm.setServer(local);
      } else {
        this.saveError.set(errorMessage(err));
      }
      return false;
    } finally {
      this.saving.set(false);
    }
  }

  // ---------- Relations ----------
  relationsOpen = signal(false);
  relations: Relation[] = [];
  readonly relForm = new FormErrors(() => {
    const errs: Record<string, string> = {};
    this.relations.forEach((r, i) => {
      errs[`${i}.name`] = r.name.trim() ? (bytes(r.name) > 100 ? 'Use at most 100 characters' : '') : 'Name is required';
      errs[`${i}.relation`] = r.relation.trim() ? '' : 'Relation is required';
      errs[`${i}.phone`] = r.phone.trim() && !PHONE.test(r.phone.trim()) ? 'Enter a phone number with 6 to 20 digits' : '';
    });
    return errs;
  });
  readonly relErrors = this.relForm.shown;
  readonly relationOptions = ['Spouse', 'Father', 'Mother', 'Son', 'Daughter', 'Brother', 'Sister', 'Guardian', 'Friend', 'Other'];

  editRelations() {
    this.relations = (this.details()?.relations ?? []).map(r => ({ ...r }));
    if (!this.relations.length) this.relations.push({ name: '', relation: '', phone: '' });
    this.relForm.reset();
    this.saveError.set('');
    this.relationsOpen.set(true);
  }

  setRel(i: number, key: keyof Relation, value: string) {
    this.relations[i][key] = value;
    this.saveError.set('');
    this.relForm.touch(`${i}.${key}`);
  }

  addRelation() {
    if (this.relations.length >= 10) return;
    this.relations.push({ name: '', relation: '', phone: '' });
    this.relForm.refresh();
  }

  removeRelation(i: number) {
    this.relations.splice(i, 1);
    this.relForm.reset();
    this.relForm.refresh();
  }

  async saveRelations() {
    const d = this.details();
    if (!d || this.saving()) return;
    // Rows left completely empty are dropped rather than reported
    this.relations = this.relations.filter(r => r.name.trim() || r.relation.trim() || r.phone.trim());
    if (!this.relForm.submit()) return;
    const next = structuredClone(d);
    next.relations = this.relations.map(r => ({ name: r.name.trim(), relation: r.relation.trim(), phone: r.phone.trim() }));
    if (await this.putDetails(next, 'relations')) {
      this.relationsOpen.set(false);
      this.toast('Relations saved');
    }
  }

  // ---------- Documents ----------
  docOpen = signal(false);
  docId = signal<number | null>(null);
  docDraft: Record<string, string> = {};
  docFile = signal<File | null>(null);
  docFileName = signal('');   // the file already attached when editing
  docError = signal('');
  readonly docForm = new FormErrors(() => {
    const errs: Record<string, string> = {};
    for (const f of this.folderInfo().fields) errs[f.key] = checkField(f, this.docDraft[f.key] ?? '');
    const d = this.docDraft;
    if (this.folder() === 'degree' && d['yearOfJoining'] && d['yearOfCompletion'] && !errs['yearOfCompletion'] && d['yearOfCompletion'] < d['yearOfJoining']) {
      errs['yearOfCompletion'] = 'Completion must be after joining';
    }
    if (this.folder() === 'experience' && d['from'] && d['to'] && !errs['to'] && d['to'] < d['from']) errs['to'] = 'End must be after the start';
    return errs;
  });
  readonly docErrors = this.docForm.shown;

  addDocument() {
    this.docId.set(null);
    this.docDraft = {};
    for (const f of this.folderInfo().fields) this.docDraft[f.key] = '';
    this.docFile.set(null);
    this.docFileName.set('');
    this.docError.set('');
    this.docForm.reset();
    this.docOpen.set(true);
  }

  editDocument(d: UserDocument) {
    this.docId.set(d.id);
    this.docDraft = {};
    for (const f of this.folderInfo().fields) this.docDraft[f.key] = d.details[f.key] ?? '';
    this.docFile.set(null);
    this.docFileName.set(d.fileName ?? '');
    this.docError.set('');
    this.docForm.reset();
    this.docOpen.set(true);
  }

  setDoc(key: string, value: string) {
    this.docDraft[key] = value;
    this.docError.set('');
    this.docForm.touch(key);
  }

  pickFile(input: HTMLInputElement) {
    const file = input.files?.[0] ?? null;
    input.value = '';
    if (!file) return;
    if (!['application/pdf', 'image/jpeg', 'image/png'].includes(file.type)) {
      this.docForm.setServerField('file', 'Allowed files: PDF, JPG, JPEG, PNG');
      return;
    }
    if (file.size > 10 * 1024 * 1024) {
      this.docForm.setServerField('file', 'Attach a file up to 10 MB');
      return;
    }
    this.docForm.setServerField('file', '');
    this.docFile.set(file);
  }

  clearFile() {
    this.docFile.set(null);
    this.docForm.setServerField('file', '');
  }

  async saveDocument() {
    if (this.saving()) return;
    if (!this.docForm.submit()) return;
    const body = new FormData();
    body.append('folder', this.folder());
    body.append('details', JSON.stringify(this.docDraft));
    const file = this.docFile();
    if (file) body.append('file', file, file.name);
    this.saving.set(true);
    this.docError.set('');
    try {
      const id = this.docId();
      const saved = await firstValueFrom(id === null
        ? this.http.post<UserDocument>('/api/profile/documents', body)
        : this.http.put<UserDocument>(`/api/profile/documents/${id}`, body));
      this.profile.update(p => {
        if (!p) return p;
        const docs = id === null ? [...p.documents, saved] : p.documents.map(d => (d.id === id ? saved : d));
        return { ...p, documents: docs };
      });
      this.docOpen.set(false);
      this.toast(id === null ? 'Added' : 'Saved');
    } catch (err) {
      const fields = err instanceof HttpErrorResponse ? (err.error as ApiError | null)?.fields : undefined;
      if (fields) this.docForm.setServer(fields);
      else this.docError.set(errorMessage(err));
    } finally {
      this.saving.set(false);
    }
  }

  removing = signal<UserDocument | null>(null);

  async confirmRemove() {
    const d = this.removing();
    if (!d || this.saving()) return;
    this.saving.set(true);
    try {
      await firstValueFrom(this.http.delete(`/api/profile/documents/${d.id}`));
      this.profile.update(p => (p ? { ...p, documents: p.documents.filter(x => x.id !== d.id) } : p));
      this.removing.set(null);
      this.toast('Removed');
    } catch (err) {
      this.toast(errorMessage(err, 'Could not remove the entry.'), true);
      this.removing.set(null);
    } finally {
      this.saving.set(false);
    }
  }

  @HostListener('document:keydown.escape')
  onEscape() {
    if (this.removing()) this.removing.set(null);
    else if (this.docOpen()) this.docOpen.set(false);
    else if (this.relationsOpen()) this.relationsOpen.set(false);
    else if (this.editing()) this.editing.set(null);
  }
}
