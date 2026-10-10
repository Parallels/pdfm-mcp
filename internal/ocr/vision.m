// vision.m — Apple Vision OCR for pdfm-mcp (spec 022).
//
// One C entry point: image bytes in, one strict-JSON document out
// ({"width":W,"height":H,"lines":[{"text","confidence","box"}]}), identical
// to the retired pdfm-ocr sidecar's wire format so internal/ocr.Parse is
// reused unchanged. No globals, no network, no file system access.
#import <CoreGraphics/CoreGraphics.h>
#import <Foundation/Foundation.h>
#import <ImageIO/ImageIO.h>
#import <Vision/Vision.h>

static void fillError(char *errbuf, int errbuf_len, NSString *msg) {
    if (errbuf == NULL || errbuf_len <= 0) return;
    if (msg == nil || msg.length == 0) msg = @"unknown error";
    const char *utf8 = msg.UTF8String;
    if (utf8 == NULL) utf8 = "";
    int n = (int)strlen(utf8);
    if (n > errbuf_len - 1) n = errbuf_len - 1;
    memcpy(errbuf, utf8, (size_t)n);
    errbuf[n] = '\0';
}

// pdfm_vision_recognize runs VNRecognizeTextRequest over the image.
// Returns a malloc'd UTF-8 JSON document (free with free()) or NULL with a
// bounded message in errbuf. At most 5000 lines are collected (parity with
// the Go-side MaxLines cap).
char *pdfm_vision_recognize(const unsigned char *data, int len,
                            int fast, const char *lang_csv,
                            char *errbuf, int errbuf_len) {
    if (data == NULL || len <= 0) {
        fillError(errbuf, errbuf_len, @"empty image");
        return NULL;
    }

    CGImageRef img = NULL;
    @try {
        NSData *nsdata = [NSData dataWithBytes:data length:(NSUInteger)len];
        CGImageSourceRef src = CGImageSourceCreateWithData((__bridge CFDataRef)nsdata, NULL);
        if (src != NULL) {
            img = CGImageSourceCreateImageAtIndex(src, 0, NULL);
            CFRelease(src);
        }
        if (img == NULL) {
            fillError(errbuf, errbuf_len, @"not a readable PNG or JPEG image");
            return NULL;
        }

        VNImageRequestHandler *handler =
            [[VNImageRequestHandler alloc] initWithCGImage:img options:@{}];

        VNRecognizeTextRequest *req = [[VNRecognizeTextRequest alloc] init];
        req.recognitionLevel = fast ? VNRequestTextRecognitionLevelFast
                                    : VNRequestTextRecognitionLevelAccurate;
        req.usesLanguageCorrection = YES;
        if (lang_csv != NULL && strlen(lang_csv) > 0) {
            NSMutableArray *langs = [NSMutableArray array];
            NSString *csv = [NSString stringWithUTF8String:lang_csv];
            for (NSString *t in [csv componentsSeparatedByString:@","]) {
                t = [t stringByTrimmingCharactersInSet:
                         [NSCharacterSet whitespaceCharacterSet]];
                if (t.length > 0) [langs addObject:t];
            }
            if (langs.count > 0) {
                req.recognitionLanguages = [langs copy];
            }
        }

        NSError *verr = nil;
        if (![handler performRequests:@[req] error:&verr]) {
            CGImageRelease(img);
            fillError(errbuf, errbuf_len,
                      verr.localizedDescription ?: @"vision recognition failed");
            return NULL;
        }

        NSInteger W = (NSInteger)CGImageGetWidth(img);
        NSInteger H = (NSInteger)CGImageGetHeight(img);

        NSMutableArray *lines = [NSMutableArray array];
        for (VNRecognizedTextObservation *ann in req.results) {
            if (lines.count >= 5000) break;
            VNRecognizedText *cand = [ann topCandidates:1].firstObject;
            if (cand == nil) continue;
            // Vision boxes are normalized with a bottom-left origin; convert
            // to integer pixels with a top-left origin, clamped to the image.
            CGRect b = ann.boundingBox;
            NSInteger x0 = (NSInteger)llround(b.origin.x * W);
            NSInteger x1 = (NSInteger)llround((b.origin.x + b.size.width) * W);
            NSInteger y0 = (NSInteger)llround((1.0 - (b.origin.y + b.size.height)) * H);
            NSInteger y1 = (NSInteger)llround((1.0 - b.origin.y) * H);
            if (x0 < 0) x0 = 0;
            if (y0 < 0) y0 = 0;
            if (x1 > W) x1 = W;
            if (y1 > H) y1 = H;
            if (x1 - x0 < 1 || y1 - y0 < 1) {
                if (x0 + 1 > W || y0 + 1 > H) continue;  // degenerate at an edge
                x1 = x0 + 1;
                y1 = y0 + 1;
            }
            [lines addObject:@{
                @"text": cand.string ?: @"",
                @"confidence": @((double)cand.confidence),
                @"box": @[@(x0), @(y0), @(x1), @(y1)],
            }];
        }
        CGImageRelease(img);
        img = NULL;

        NSDictionary *doc = @{@"width": @(W), @"height": @(H), @"lines": lines};
        NSError *jerr = nil;
        NSData *json = [NSJSONSerialization dataWithJSONObject:doc options:0
                                                         error:&jerr];
        if (json == NULL) {
            fillError(errbuf, errbuf_len,
                      jerr.localizedDescription ?: @"json encoding failed");
            return NULL;
        }
        // NUL-terminated: Go's C.GoString reads to the first NUL.
        char *out = malloc(json.length + 1);
        if (out == NULL) {
            fillError(errbuf, errbuf_len, @"out of memory");
            return NULL;
        }
        memcpy(out, json.bytes, json.length);
        out[json.length] = '\0';
        return out;
    } @catch (NSException *e) {
        if (img != NULL) CGImageRelease(img);
        fillError(errbuf, errbuf_len, e.reason ?: @"vision exception");
        return NULL;
    }
}
