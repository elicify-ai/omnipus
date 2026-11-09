/*
 * Omnipus - Ultra-lightweight personal AI agent
 * License: MIT
 * Copyright (c) 2026 Omnipus contributors
 *
 * omnipus-pdfium — the out-of-process PDF renderer of office preview
 * (docs/internal/specs/office-preview-spec.md, AD-2 "Protocol v1").
 *
 * Reads exactly one request from stdin and writes framed output to stdout.
 * It opens no file and no socket: the PDF arrives on stdin, frames leave on
 * stdout, and pdfium's system-font lookup is replaced by an empty provider so
 * no font file is read either (non-embedded fonts render with pdfium's
 * built-in substitutes; S-1 positive control). The gateway never trusts this
 * program's exit code: success is a DONE frame whose count matches.
 *
 * Exit codes: 0 done, 2 bad request, 3 truncated input (stdin EOF before
 * input_len bytes), 4 document error (ERROR frame sent), 5 parent gone (stdin
 * EOF after the request; stopped at a page boundary).
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "fpdf_formfill.h"
#include "fpdf_sysfontinfo.h"
#include "fpdf_text.h"
#include "fpdfview.h"
#include "zlib.h"

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#else
#include <poll.h>
#include <unistd.h>
#endif

#ifndef OMNIPUS_PDFIUM_BUILD_ID
#error "OMNIPUS_PDFIUM_BUILD_ID must be defined by the build"
#endif

#define PROTOCOL_VERSION 1
#define MAX_LONG_EDGE 2000u
#define MAX_PAGES_PER_CALL 20u
#define MAX_PAGE_FRAME (4u << 20)
#define MAX_TEXT_FRAME (1u << 20)
#define MAX_TEXT_TOTAL (4u << 20)
#define OPDF_MAX_INPUT (200ull << 20)
#define PNG_LEVEL 3 /* S-1: median 27 ms per page; stb alone 89-95 ms */
#define PNG_FILTER_UP 2

enum { F_META = 1, F_PAGE = 2, F_TEXT = 3, F_ERROR = 4, F_DONE = 5 };
enum { OP_INFO = 1, OP_RENDER = 2, OP_TEXT = 3 };

static uint32_t frames;

static void put_u32(uint8_t *p, uint32_t v) {
  p[0] = (uint8_t)v;
  p[1] = (uint8_t)(v >> 8);
  p[2] = (uint8_t)(v >> 16);
  p[3] = (uint8_t)(v >> 24);
}
static uint32_t get_u32(const uint8_t *p) {
  return (uint32_t)p[0] | ((uint32_t)p[1] << 8) | ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}
static uint16_t get_u16(const uint8_t *p) { return (uint16_t)(p[0] | (p[1] << 8)); }

static void frame(uint8_t type, const void *a, uint32_t alen, const void *b, uint32_t blen) {
  uint8_t h[5];
  h[0] = type;
  put_u32(h + 1, alen + blen);
  fwrite(h, 1, 5, stdout);
  if (alen) fwrite(a, 1, alen, stdout);
  if (blen) fwrite(b, 1, blen, stdout);
  fflush(stdout);
  frames++;
}

/* code and msg are compile-time constants without characters that need JSON
 * escaping. */
static void fail(const char *code, const char *msg, int rc) {
  char buf[512];
  int n = snprintf(buf, sizeof buf, "{\"code\":\"%s\",\"message\":\"%s\"}", code, msg);
  if (n < 0) n = 0;
  if (n >= (int)sizeof buf) n = (int)sizeof buf - 1;
  frame(F_ERROR, buf, (uint32_t)n, NULL, 0);
  exit(rc);
}

static void done(void) {
  uint8_t d[4];
  put_u32(d, frames + 1);
  frame(F_DONE, d, 4, NULL, 0);
}

/* The parent keeps stdin open for the whole call; EOF after the request means
 * the parent is gone (AD-2). Unix only: on Windows the launcher's job ends the
 * process instead. */
static int parent_gone(void) {
#ifndef _WIN32
  struct pollfd p;
  p.fd = 0;
  p.events = POLLIN;
  p.revents = 0;
  if (poll(&p, 1, 0) > 0) {
    char c;
    if (read(0, &c, 1) <= 0) return 1;
  }
#endif
  return 0;
}

static int read_full(uint8_t *buf, uint64_t n) {
  uint64_t got = 0;
  while (got < n) {
    size_t want = (size_t)((n - got) > (1u << 20) ? (1u << 20) : (n - got));
    size_t r = fread(buf + got, 1, want, stdin);
    if (r == 0) return 0;
    got += r;
  }
  return 1;
}

/* ---- PNG (RGBA, 8-bit) over zlib, bounded by the PAGE frame limit ---- */

typedef struct {
  uint8_t *p;
  size_t n, cap;
  int over;
} membuf;

static void sink(membuf *m, const void *data, size_t size) {
  if (m->over) return;
  if (m->n + size > MAX_PAGE_FRAME - 12) {
    m->over = 1;
    return;
  }
  if (m->n + size > m->cap) {
    size_t cap = (m->n + size) * 2;
    if (cap > MAX_PAGE_FRAME) cap = MAX_PAGE_FRAME;
    uint8_t *np = realloc(m->p, cap);
    if (!np) {
      m->over = 1;
      return;
    }
    m->p = np;
    m->cap = cap;
  }
  memcpy(m->p + m->n, data, size);
  m->n += size;
}

static void be32(uint8_t *p, uint32_t v) {
  p[0] = (uint8_t)(v >> 24);
  p[1] = (uint8_t)(v >> 16);
  p[2] = (uint8_t)(v >> 8);
  p[3] = (uint8_t)v;
}

static void chunk(membuf *m, const char *type, const uint8_t *d, uint32_t n) {
  uint8_t h[8], c4[4];
  be32(h, n);
  memcpy(h + 4, type, 4);
  sink(m, h, 8);
  if (n) sink(m, d, n);
  uLong c = crc32(0, (const Bytef *)type, 4);
  if (n) c = crc32(c, d, n);
  be32(c4, (uint32_t)c);
  sink(m, c4, 4);
}

/* Pixels arrive as RGBA (FPDF_REVERSE_BYTE_ORDER). Returns 0 on success. */
static int encode_png(membuf *m, const uint8_t *px, int w, int h, int stride) {
  static const uint8_t sig[8] = {137, 80, 78, 71, 13, 10, 26, 10};
  uint8_t ihdr[13];
  sink(m, sig, 8);
  be32(ihdr, (uint32_t)w);
  be32(ihdr + 4, (uint32_t)h);
  ihdr[8] = 8;  /* bit depth */
  ihdr[9] = 6;  /* RGBA */
  ihdr[10] = 0; /* deflate */
  ihdr[11] = 0; /* adaptive filtering */
  ihdr[12] = 0; /* no interlace */
  chunk(m, "IHDR", ihdr, 13);

  z_stream z;
  memset(&z, 0, sizeof z);
  if (deflateInit2(&z, PNG_LEVEL, Z_DEFLATED, 15, 8, Z_DEFAULT_STRATEGY) != Z_OK) return -1;
  size_t rowlen = (size_t)w * 4 + 1;
  uint8_t *row = malloc(rowlen);
  uint8_t *out = malloc(1u << 18);
  if (!row || !out) {
    free(row);
    free(out);
    deflateEnd(&z);
    return -1;
  }
  for (int y = 0; y < h && !m->over; y++) {
    const uint8_t *r = px + (size_t)y * (size_t)stride;
    if (y > 0) {
      const uint8_t *prev = px + (size_t)(y - 1) * (size_t)stride;
      row[0] = PNG_FILTER_UP;
      for (int i = 0; i < w * 4; i++) row[1 + i] = (uint8_t)(r[i] - prev[i]);
    } else {
      row[0] = 0;
      memcpy(row + 1, r, (size_t)w * 4);
    }
    z.next_in = row;
    z.avail_in = (uInt)rowlen;
    do {
      z.next_out = out;
      z.avail_out = 1u << 18;
      deflate(&z, y == h - 1 ? Z_FINISH : Z_NO_FLUSH);
      uint32_t n = (1u << 18) - z.avail_out;
      if (n) chunk(m, "IDAT", out, n);
    } while (z.avail_out == 0 && !m->over);
  }
  deflateEnd(&z);
  free(row);
  free(out);
  chunk(m, "IEND", NULL, 0);
  return 0;
}

/* ---- text ---- */

/* UTF-16LE (pdfium) to UTF-8; unpaired surrogates become U+FFFD so the
 * output is always valid UTF-8, which the gateway's reader requires. */
static size_t u16_to_u8(const unsigned short *s, size_t n, char *out) {
  size_t o = 0;
  for (size_t i = 0; i < n; i++) {
    uint32_t c = s[i];
    if (c >= 0xD800 && c < 0xDC00) {
      if (i + 1 < n && s[i + 1] >= 0xDC00 && s[i + 1] < 0xE000) {
        c = 0x10000 + ((c - 0xD800) << 10) + (s[i + 1] - 0xDC00);
        i++;
      } else {
        c = 0xFFFD;
      }
    } else if (c >= 0xDC00 && c < 0xE000) {
      c = 0xFFFD;
    }
    if (c < 0x80) {
      out[o++] = (char)c;
    } else if (c < 0x800) {
      out[o++] = (char)(0xC0 | (c >> 6));
      out[o++] = (char)(0x80 | (c & 63));
    } else if (c < 0x10000) {
      out[o++] = (char)(0xE0 | (c >> 12));
      out[o++] = (char)(0x80 | ((c >> 6) & 63));
      out[o++] = (char)(0x80 | (c & 63));
    } else {
      out[o++] = (char)(0xF0 | (c >> 18));
      out[o++] = (char)(0x80 | ((c >> 12) & 63));
      out[o++] = (char)(0x80 | ((c >> 6) & 63));
      out[o++] = (char)(0x80 | (c & 63));
    }
  }
  return o;
}

/* Largest length <= n that does not split a UTF-8 sequence. */
static size_t utf8_cut(const char *s, size_t n) {
  size_t i = n;
  while (i > 0 && ((unsigned char)s[i] & 0xC0) == 0x80) i--;
  return i;
}

static void emit_text(FPDF_PAGE page, uint32_t pg, size_t *text_total) {
  FPDF_TEXTPAGE tp = FPDFText_LoadPage(page);
  int nch = tp ? FPDFText_CountChars(tp) : 0;
  size_t n8 = 0;
  char *u8 = NULL;
  if (nch > 0) {
    unsigned short *u = malloc(((size_t)nch + 1) * 2);
    u8 = malloc((size_t)nch * 4 + 4);
    if (!u || !u8) fail("oom", "text allocation failed", 4);
    int got = FPDFText_GetText(tp, 0, nch, u);
    /* got counts the terminating NUL */
    if (got > 1) n8 = u16_to_u8(u, (size_t)(got - 1), u8);
    free(u);
  }
  size_t room = MAX_TEXT_TOTAL - *text_total;
  size_t limit = MAX_TEXT_FRAME - 4;
  if (room < limit) limit = room;
  if (n8 > limit) n8 = utf8_cut(u8, limit);
  *text_total += n8;
  uint8_t p4[4];
  put_u32(p4, pg);
  frame(F_TEXT, p4, 4, u8, (uint32_t)n8);
  free(u8);
  if (tp) FPDFText_ClosePage(tp);
}

/* ---- render ---- */

static void emit_page(FPDF_PAGE page, FPDF_FORMHANDLE form, uint32_t pg, uint32_t maxedge) {
  double pw = FPDF_GetPageWidthF(page), ph = FPDF_GetPageHeightF(page);
  if (!(pw > 0) || !(ph > 0)) fail("page_failed", "page has no size", 4);
  double scale = 2.0;
  double longest = (pw > ph ? pw : ph) * scale;
  if (longest > maxedge) scale = scale * maxedge / longest;
  int w = (int)(pw * scale + 0.5), h = (int)(ph * scale + 0.5);
  if (w < 1) w = 1;
  if (h < 1) h = 1;
  /* bitmap size checked before allocation (AD-2) */
  if (w > (int)maxedge || h > (int)maxedge) fail("pixel_cap", "rendering stopped: pixel limit exceeded", 4);
  if ((uint64_t)w * (uint64_t)h * 4u > (uint64_t)MAX_LONG_EDGE * MAX_LONG_EDGE * 4u)
    fail("pixel_cap", "rendering stopped: pixel limit exceeded", 4);
  FPDF_BITMAP bmp = FPDFBitmap_Create(w, h, 0);
  if (!bmp) fail("oom", "rendering stopped: memory exceeded", 4);
  FPDFBitmap_FillRect(bmp, 0, 0, w, h, 0xFFFFFFFF);
  FPDF_RenderPageBitmap(bmp, page, 0, 0, w, h, 0, FPDF_ANNOT | FPDF_REVERSE_BYTE_ORDER);
  if (form) FPDF_FFLDraw(form, bmp, page, 0, 0, w, h, 0, FPDF_ANNOT | FPDF_REVERSE_BYTE_ORDER);
  membuf m;
  memset(&m, 0, sizeof m);
  int rc = encode_png(&m, FPDFBitmap_GetBuffer(bmp), w, h, FPDFBitmap_GetStride(bmp));
  FPDFBitmap_Destroy(bmp);
  if (rc != 0) fail("oom", "rendering stopped: memory exceeded", 4);
  if (m.over) fail("frame_too_large", "rendering stopped: page image exceeds 4 MiB", 4);
  uint8_t h12[12];
  put_u32(h12, pg);
  put_u32(h12 + 4, (uint32_t)w);
  put_u32(h12 + 8, (uint32_t)h);
  frame(F_PAGE, h12, 12, m.p, (uint32_t)m.n);
  free(m.p);
}

int main(void) {
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  uint8_t h[32];
  if (!read_full(h, 32)) return 3;
  if (memcmp(h, "OPDF", 4) != 0) fail("bad_magic", "bad magic", 2);
  if (get_u16(h + 4) != PROTOCOL_VERSION) fail("bad_version", "unsupported protocol version", 2);
  uint16_t op = get_u16(h + 6);
  uint32_t first = get_u32(h + 8), last = get_u32(h + 12), maxedge = get_u32(h + 16), flags = get_u32(h + 20);
  uint64_t inlen = (uint64_t)get_u32(h + 24) | ((uint64_t)get_u32(h + 28) << 32);
  if (op < OP_INFO || op > OP_TEXT) fail("bad_op", "unknown op", 2);
  if (inlen == 0 || inlen > OPDF_MAX_INPUT) fail("bad_length", "input length out of range", 2);
  if (op != OP_INFO && (first < 1 || last < first || last - first >= MAX_PAGES_PER_CALL))
    fail("bad_range", "bad page range", 2);
  if (op == OP_RENDER && (maxedge == 0 || maxedge > MAX_LONG_EDGE)) fail("bad_edge", "bad max long edge", 2);
  uint8_t *in = malloc((size_t)inlen);
  if (!in) fail("oom", "input allocation failed", 2);
  if (!read_full(in, inlen)) return 3;

  FPDF_LIBRARY_CONFIG cfg;
  memset(&cfg, 0, sizeof cfg);
  cfg.version = 2;
  FPDF_InitLibraryWithConfig(&cfg);
  /* Empty system-font provider: pdfium reads no font file (MV-6). */
  static FPDF_SYSFONTINFO no_fonts;
  memset(&no_fonts, 0, sizeof no_fonts);
  no_fonts.version = 1;
  FPDF_SetSystemFontInfo(&no_fonts);

  char meta[256];
  FPDF_DOCUMENT doc = FPDF_LoadMemDocument64(in, (size_t)inlen, NULL);
  if (!doc) {
    if (FPDF_GetLastError() == FPDF_ERR_PASSWORD) {
      int mn = snprintf(meta, sizeof meta,
                        "{\"protocol_version\":%d,\"build_id\":\"%s\",\"page_count\":0,\"encrypted\":true}",
                        PROTOCOL_VERSION, OMNIPUS_PDFIUM_BUILD_ID);
      frame(F_META, meta, (uint32_t)mn, NULL, 0);
      fail("encrypted", "password-protected documents cannot be previewed", 4);
    }
    fail("open_failed", "the PDF could not be opened (damaged or unsupported)", 4);
  }
  int pc = FPDF_GetPageCount(doc);
  if (pc < 0) pc = 0;
  int mn = snprintf(meta, sizeof meta,
                    "{\"protocol_version\":%d,\"build_id\":\"%s\",\"page_count\":%d,\"encrypted\":false}",
                    PROTOCOL_VERSION, OMNIPUS_PDFIUM_BUILD_ID, pc);
  frame(F_META, meta, (uint32_t)mn, NULL, 0);
  if (op == OP_INFO) {
    done();
    return 0;
  }
  if (first > (uint32_t)pc) {
    /* The gateway clamps after info; a request beyond the document is
     * answered with no pages. */
    done();
    return 0;
  }
  if (last > (uint32_t)pc) last = (uint32_t)pc;

  FPDF_FORMHANDLE form = NULL;
  FPDF_FORMFILLINFO ffi;
  if (op == OP_RENDER && (flags & 1u)) {
    memset(&ffi, 0, sizeof ffi);
    ffi.version = 1;
    form = FPDFDOC_InitFormFillEnvironment(doc, &ffi);
    if (form) FPDF_SetFormFieldHighlightAlpha(form, 0);
  }
  size_t text_total = 0;
  for (uint32_t pg = first; pg <= last; pg++) {
    if (parent_gone()) return 5;
    FPDF_PAGE page = FPDF_LoadPage(doc, (int)pg - 1);
    if (!page) fail("page_failed", "the PDF could not be opened (damaged or unsupported)", 4);
    if (op == OP_RENDER) {
      emit_page(page, form, pg, maxedge);
    } else {
      emit_text(page, pg, &text_total);
    }
    FPDF_ClosePage(page);
    if (op == OP_TEXT && text_total >= MAX_TEXT_TOTAL) break;
  }
  done();
  return 0;
}
