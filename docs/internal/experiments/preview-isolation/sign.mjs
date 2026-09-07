import fs from 'node:fs';
import * as pdfjs from './node_modules/pdfjs-dist/legacy/build/pdf.mjs';
pdfjs.GlobalWorkerOptions.workerSrc = new URL('./node_modules/pdfjs-dist/legacy/build/pdf.worker.mjs', import.meta.url).href;

const N = NaN;
// A hand-drawn-looking stroke: [_,_,_,_,x0,y0, NaN,_,_,_,x1,y1, ...] per the 6.x format
const pts = [[60,120],[75,145],[95,100],[115,140],[140,95],[165,135],[190,105],[215,130],[245,115]];
const line = [N,N,N,N, pts[0][0], pts[0][1]];
for (let i=1;i<pts.length;i++) line.push(N,N,N,N, pts[i][0], pts[i][1]);

const doc = await pdfjs.getDocument({data:new Uint8Array(fs.readFileSync('form.pdf'))}).promise;
doc.annotationStorage.setValue('pdfjs_internal_editor_0', {
  annotationType: pdfjs.AnnotationEditorType.INK,
  color:[0,0,0], thickness:2, opacity:1,
  paths:{ lines:[line], points:[pts.flat()] },
  pageIndex:0, rect:[50,85,255,155], rotation:0,
});
const out = await doc.saveDocument();
fs.writeFileSync('out-signed.pdf', out);
console.log('signed pdf bytes:', out.length);
