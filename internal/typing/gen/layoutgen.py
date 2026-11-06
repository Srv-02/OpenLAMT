#!/usr/bin/env python3
"""Generates internal/typing/layout_tables.go from XKB keyboard data.

Only facts are taken from xkeyboard-config (via libxkbcommon): which character a key
produces at which level. No XKB file is copied. Usage, from the repository root
(needs libxkbcommon.so.0 and the xkeyboard-config data installed):

    python3 internal/typing/gen/layoutgen.py emit > internal/typing/layout_tables.go

XKB differs from the Windows layouts in places; KEEP (AltGr levels) and DEAD_AT (keys that are
dead keys on Windows) record the corrections. Review the diff of layout_tables.go after a run.
"""
import ctypes, sys
x = ctypes.CDLL("libxkbcommon.so.0")
x.xkb_context_new.restype = ctypes.c_void_p
x.xkb_keymap_new_from_names.restype = ctypes.c_void_p
x.xkb_keymap_new_from_names.argtypes = [ctypes.c_void_p, ctypes.c_void_p, ctypes.c_int]
x.xkb_keymap_key_get_syms_by_level.argtypes = [ctypes.c_void_p, ctypes.c_uint32, ctypes.c_uint32, ctypes.c_uint32, ctypes.POINTER(ctypes.POINTER(ctypes.c_uint32))]
x.xkb_keymap_key_get_syms_by_level.restype = ctypes.c_int
x.xkb_keymap_num_layouts_for_key.argtypes=[ctypes.c_void_p, ctypes.c_uint32]
x.xkb_keysym_get_name.argtypes=[ctypes.c_uint32, ctypes.c_char_p, ctypes.c_size_t]
x.xkb_keymap_num_levels_for_key.argtypes=[ctypes.c_void_p, ctypes.c_uint32, ctypes.c_uint32]
ctx = ctypes.c_void_p(x.xkb_context_new(0))
class Names(ctypes.Structure):
    _fields_=[(n,ctypes.c_char_p) for n in ("rules","model","layout","variant","options")]
def keymap(layout, variant=""):
    n = Names(b"evdev", b"pc105", layout.encode(), variant.encode(), b"")
    km = x.xkb_keymap_new_from_names(ctx, ctypes.byref(n), 0)
    assert km, layout
    return ctypes.c_void_p(km)
def syms(km, kc, level):
    if x.xkb_keymap_num_levels_for_key(km, kc, 0) <= level: return []
    p = ctypes.POINTER(ctypes.c_uint32)()
    n = x.xkb_keymap_key_get_syms_by_level(km, kc, 0, level, ctypes.byref(p))
    return [p[i] for i in range(n)]
def name(s):
    b = ctypes.create_string_buffer(64); x.xkb_keysym_get_name(s, b, 64); return b.value.decode()
# evdev keycode (xkb = evdev+8) for US-position keys, row by row
EV = {}
row = lambda start, n: list(range(start, start+n))
us_pos = (row(41,1) + row(2,12) + row(16,12) + row(43,1) + row(30,11) + row(44,10))  # TLDE, AE01-12, AD01-12, BKSL, AC01-11, AB01-10
ISO = 86
KEYS = us_pos
x.xkb_keysym_to_utf32.argtypes=[ctypes.c_uint32]; x.xkb_keysym_to_utf32.restype=ctypes.c_uint32
DEAD = {"dead_circumflex":"^","dead_grave":"`","dead_acute":"´","dead_diaeresis":"¨","dead_tilde":"~","dead_cedilla":"¸","dead_caron":"ˇ"}
def ok(c):
    return 0x20<c<0x7f or 0xa1<=c<=0x17f or c==0x20ac or 0x400<=c<=0x4ff or c==0x2116
LAYOUTS = [  # code, Go const, xkb layout, variant
 ("ch-de","LayoutCHDE","ch","de"),("ch-fr","LayoutCHFR","ch","fr"),("fr","LayoutFR","fr",""),
 ("be","LayoutBE","be",""),("uk","LayoutUK","gb",""),("it","LayoutIT","it",""),("es","LayoutES","es",""),
 ("pt","LayoutPT","pt",""),("se-fi","LayoutSEFI","se",""),("no","LayoutNO","no",""),("dk","LayoutDK","dk",""),
 ("br","LayoutBR","br",""),("ru","LayoutRU","ru",""),("jp","LayoutJP","jp","")]
def level(km, ev, l):
    s = syms(km, ev+8, l)
    if len(s)!=1: return None
    n = name(s[0])
    if n in DEAD: return ("d", DEAD[n])
    c = x.xkb_keysym_to_utf32(s[0])
    if c and ok(c): return ("k", chr(c))
    return None
def lit(l):
    if l is None: return "none"
    f = "d" if l[0]=="d" else "lv"
    return "%s(%s)" % (f, repr_rune(l[1]))
def repr_rune(ch):
    if ch=="'": return r"'\''"
    if ch=="\\": return r"'\\'"
    return "'%s'" % ch
# AltGr entries kept per layout (position = US key). XKB carries many Linux-only AltGr extras
# that the common Windows and firmware layouts do not have; those would type a wrong character
# silently, so everything not listed here is treated as not typable.
CH = "2 3 7 e [ ] \\ ' - ="
KEEP = {
 "ch-de": CH, "ch-fr": CH,
 "fr": "2 3 4 5 6 7 8 9 0 - = e",
 "be": "1 2 3 6 9 0 [ ] e \\ ' /",
 "uk": "4",
 "it": "- = e [ ] ; '",
 "es": "` 1 2 3 4 e [ ] \\ '",
 "pt": "2 3 4 7 8 9 0 e",
 "se-fi": "2 3 4 7 8 9 0 - e ]",
 "no": "2 3 4 7 8 9 0 e ] =",
 "dk": "2 3 4 7 8 9 0 e ] =",
 "br": "4 q w =",
 "ru": "", "jp": "",
}
KEEP4 = {"it": "[ ]"}
# Keys that XKB has as plain characters but the Windows layouts (KBDFR, KBDSP) make with dead keys:
# (layout, US position, level index) -> character. Type sends the dead key and Space.
DEAD_AT = {("fr", "2", 2): "~", ("fr", "7", 2): "`", ("es", "4", 2): "~"}
def emit():
    us = keymap("us")
    posof = [chr(x.xkb_keysym_to_utf32(syms(us, ev+8, 0)[0])) for ev in KEYS]
    out = ["// Code generated from the XKB symbol data (facts only: which character a key produces); DO NOT EDIT.\n",
           "// Source: xkeyboard-config symbols ch, fr, be, gb, it, es, pt, se, no, dk, br, ru, jp. Base and Shift\n// levels as XKB has them; AltGr levels only where Windows layouts agree (see KEEP and DEAD_AT in layoutgen.py).\n// Regenerate: python3 internal/typing/gen/layoutgen.py emit > internal/typing/layout_tables.go (see gen/layoutgen.py).\n\npackage typing\n\n"]
    out.append("var rawLayouts = map[Layout][]keyDef{\n")
    for code, const, lay, var in LAYOUTS:
        km = keymap(lay, var)
        keep, keep4 = KEEP[code].split(), KEEP4.get(code, "").split()
        out.append("\t%s: {\n" % const)
        for ev, pos in zip(KEYS, posof):
            lv = [level(km, ev, l) for l in range(4)]
            if pos not in keep: lv[2] = None
            if pos not in keep4: lv[3] = None
            if code == "jp" and pos == "0": lv[1] = None  # JIS: Shift+0 prints nothing
            if code == "fr" and pos == "`": lv[1] = None  # AZERTY: Shift+2 key prints nothing (XKB has a ~)
            for (dcode, dpos, dl), dch in DEAD_AT.items():
                if code == dcode and pos == dpos:
                    assert lv[dl] == ("k", dch), (code, pos, dl, lv[dl])
                    lv[dl] = ("d", dch)
            if all(v is None for v in lv): continue
            out.append("\t\t{%s, %s},\n" % (repr_rune(pos), ", ".join(lit(v) for v in lv)))
        out.append("\t},\n")
    out.append("}\n")
    return "".join(out)
if __name__ == "__main__" and len(sys.argv)>1 and sys.argv[1]=="emit":
    sys.stdout.write(emit())
