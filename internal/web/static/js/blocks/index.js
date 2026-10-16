// Реестр блоков экрана по порядку. Новый блок = один файл + строка здесь.
import * as forms from './forms.js';
import * as win from './window.js';
import * as kpi from './kpi.js';
import * as timeline from './timeline.js';
import * as next from './next.js';
import * as inbox from './inbox.js';
import * as categories from './categories.js';

export const blocks = [forms, win, kpi, timeline, next, inbox, categories];
