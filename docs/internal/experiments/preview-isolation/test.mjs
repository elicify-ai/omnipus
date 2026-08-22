// Verify PDF.js can (a) read form fields, (b) fill and SAVE them into the binary,
// (c) add a drawn signature (ink) and save it. Node-side, using the same API the
// viewer uses internally.
import fs from 'node:fs';
import * as pdfjs from './node_modules/pdfjs-dist/legacy/build/pdf.mjs';
pdfjs.GlobalWorkerOptions.workerSrc = new URL('./node_modules/pdfjs-dist/legacy/build/pdf.worker.mjs', import.meta.url).href;

const raw = new Uint8Array(fs.readFileSync('form.pdf'));
const doc = await pdfjs.getDocument({data: raw, useSystemFonts:true}).promise;
console.log('pages:', doc.numPages);

// (a) discover form fields
const page = await doc.getPage(1);
const annots = await page.getAnnotations({intent:'any'});
const fields = annots.filter(a=>a.subtype==='Widget');
console.log('form fields found:', fields.map(f=>({name:f.fieldName, type:f.fieldType, id:f.id, value:f.fieldValue})));
if (!fields.length) { console.log('FAIL: no widgets discovered'); process.exit(2); }

// (b) fill it, exactly as the viewer does — annotationStorage keyed by annotation id
const TYPED = 'Daniel Piatkowski';
doc.annotationStorage.setValue(fields[0].id, {value: TYPED});
const filled = await doc.saveDocument();
fs.writeFileSync('out-filled.pdf', filled);
console.log('saved out-filled.pdf bytes:', filled.length);

// (c) drawn signature = an INK annotation via the editor storage format
const doc2 = await pdfjs.getDocument({data: new Uint8Array(fs.readFileSync('form.pdf'))}).promise;
const p2 = await doc2.getPage(1);
const [ , , w, h ] = p2.view;
doc2.annotationStorage.setValue('pdfjs_internal_editor_0', {
  annotationType: pdfjs.AnnotationEditorType.INK,
  color: [0,0,0], thickness: 2, opacity: 1,
  paths: [{bezier:[60,120, 90,150, 130,90, 170,130, 210,100, 250,140], points:[60,120, 250,140]}],
  pageIndex: 0,
  rect: [55,85,255,155],
  rotation: 0,
});
const signed = await doc2.saveDocument();
fs.writeFileSync('out-signed.pdf', signed);
console.log('saved out-signed.pdf bytes:', signed.length);
console.log('TYPED_VALUE=' + TYPED);
