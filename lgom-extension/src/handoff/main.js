import { mount } from 'svelte';
import '../routes/layout.css';
import { i18nGetMessage } from '$lib/platform-api.js';
import HandoffNotice from './HandoffNotice.svelte';

document.title = i18nGetMessage('handoffPageTitle');

const target = document.getElementById('app');
if (!target) throw new Error('handoff root #app missing');

mount(HandoffNotice, { target });
