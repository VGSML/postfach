// Package erechnung parses German/EU electronic invoices: XRechnung as
// standalone XML (UBL or UN/CEFACT CII syntax) and ZUGFeRD/Factur-X (CII
// XML embedded in PDF/A-3). Output is a normalized structure so the client
// model works with fields, not raw XML. Amounts are kept as decimal
// strings exactly as they appear in the document.
package erechnung

import (
	"bytes"
	"compress/zlib"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

type Party struct {
	Name  string `json:"name,omitempty"`
	VATID string `json:"vat_id,omitempty"`
}

type Line struct {
	ID          string `json:"id,omitempty"`
	Description string `json:"description,omitempty"`
	Quantity    string `json:"quantity,omitempty"`
	Unit        string `json:"unit,omitempty"`
	NetAmount   string `json:"net_amount,omitempty"`
}

// Invoice is the normalized e-invoice view.
type Invoice struct {
	Format           string `json:"format"`            // "ubl" or "cii"
	Profile          string `json:"profile,omitempty"` // customization/guideline id
	InvoiceNumber    string `json:"invoice_number"`
	TypeCode         string `json:"type_code,omitempty"` // 380 invoice, 381 credit note, ...
	IssueDate        string `json:"issue_date,omitempty"`
	DueDate          string `json:"due_date,omitempty"`
	Currency         string `json:"currency,omitempty"`
	Seller           Party  `json:"seller"`
	Buyer            Party  `json:"buyer"`
	BuyerReference   string `json:"buyer_reference,omitempty"` // Leitweg-ID for XRechnung
	PaymentReference string `json:"payment_reference,omitempty"`
	IBAN             string `json:"iban,omitempty"`
	TotalNet         string `json:"total_net,omitempty"`
	TotalTax         string `json:"total_tax,omitempty"`
	TotalGross       string `json:"total_gross,omitempty"`
	PayableAmount    string `json:"payable_amount,omitempty"`
	Lines            []Line `json:"lines,omitempty"`
}

// ---- UBL (XRechnung UBL syntax) ---------------------------------------

// ublInvoice covers both UBL Invoice and UBL CreditNote roots: no XMLName
// pin, and the CreditNote-specific element names live in parallel fields.
type ublInvoice struct {
	CustomizationID  string   `xml:"CustomizationID"`
	ID               string   `xml:"ID"`
	IssueDate        string   `xml:"IssueDate"`
	DueDate          string   `xml:"DueDate"`
	InvoiceTypeCode  string   `xml:"InvoiceTypeCode"`
	CreditNoteType   string   `xml:"CreditNoteTypeCode"`
	DocumentCurrency string   `xml:"DocumentCurrencyCode"`
	BuyerReference   string   `xml:"BuyerReference"`
	Supplier         ublParty `xml:"AccountingSupplierParty>Party"`
	Customer         ublParty `xml:"AccountingCustomerParty>Party"`
	PaymentMeans     []struct {
		PaymentID string `xml:"PaymentID"`
		Account   struct {
			ID string `xml:"ID"`
		} `xml:"PayeeFinancialAccount"`
	} `xml:"PaymentMeans"`
	TaxTotal struct {
		TaxAmount string `xml:"TaxAmount"`
	} `xml:"TaxTotal"`
	Totals struct {
		LineExtension string `xml:"LineExtensionAmount"`
		TaxExclusive  string `xml:"TaxExclusiveAmount"`
		TaxInclusive  string `xml:"TaxInclusiveAmount"`
		Payable       string `xml:"PayableAmount"`
	} `xml:"LegalMonetaryTotal"`
	Lines   []ublLine `xml:"InvoiceLine"`
	CNLines []ublLine `xml:"CreditNoteLine"`
}

type ublLine struct {
	ID       string `xml:"ID"`
	Quantity struct {
		Value string `xml:",chardata"`
		Unit  string `xml:"unitCode,attr"`
	} `xml:"InvoicedQuantity"`
	CNQuantity struct {
		Value string `xml:",chardata"`
		Unit  string `xml:"unitCode,attr"`
	} `xml:"CreditedQuantity"`
	Amount string `xml:"LineExtensionAmount"`
	Item   struct {
		Name string `xml:"Name"`
	} `xml:"Item"`
}

type ublParty struct {
	Name       string `xml:"PartyName>Name"`
	LegalName  string `xml:"PartyLegalEntity>RegistrationName"`
	CompanyTax struct {
		CompanyID string `xml:"CompanyID"`
	} `xml:"PartyTaxScheme"`
}

func (p ublParty) party() Party {
	name := p.LegalName
	if name == "" {
		name = p.Name
	}
	return Party{Name: name, VATID: p.CompanyTax.CompanyID}
}

// ---- CII (XRechnung CII syntax, ZUGFeRD/Factur-X) ----------------------

type ciiInvoice struct {
	XMLName xml.Name `xml:"CrossIndustryInvoice"`
	Context struct {
		Guideline string `xml:"GuidelineSpecifiedDocumentContextParameter>ID"`
	} `xml:"ExchangedDocumentContext"`
	Doc struct {
		ID        string `xml:"ID"`
		TypeCode  string `xml:"TypeCode"`
		IssueDate struct {
			DateTimeString string `xml:"DateTimeString"`
		} `xml:"IssueDateTime"`
	} `xml:"ExchangedDocument"`
	Trade struct {
		Agreement struct {
			BuyerReference string   `xml:"BuyerReference"`
			Seller         ciiParty `xml:"SellerTradeParty"`
			Buyer          ciiParty `xml:"BuyerTradeParty"`
		} `xml:"ApplicableHeaderTradeAgreement"`
		Settlement struct {
			PaymentReference string `xml:"PaymentReference"`
			Currency         string `xml:"InvoiceCurrencyCode"`
			PaymentMeans     []struct {
				IBAN string `xml:"PayeePartyCreditorFinancialAccount>IBANID"`
			} `xml:"SpecifiedTradeSettlementPaymentMeans"`
			Terms struct {
				DueDate struct {
					DateTimeString string `xml:"DateTimeString"`
				} `xml:"DueDateDateTime"`
			} `xml:"SpecifiedTradePaymentTerms"`
			Summation struct {
				LineTotal  string `xml:"LineTotalAmount"`
				TaxBasis   string `xml:"TaxBasisTotalAmount"`
				TaxTotal   string `xml:"TaxTotalAmount"`
				GrandTotal string `xml:"GrandTotalAmount"`
				DuePayable string `xml:"DuePayableAmount"`
			} `xml:"SpecifiedTradeSettlementHeaderMonetarySummation"`
		} `xml:"ApplicableHeaderTradeSettlement"`
		Lines []struct {
			Doc struct {
				LineID string `xml:"LineID"`
			} `xml:"AssociatedDocumentLineDocument"`
			Product struct {
				Name string `xml:"Name"`
			} `xml:"SpecifiedTradeProduct"`
			Delivery struct {
				Quantity struct {
					Value string `xml:",chardata"`
					Unit  string `xml:"unitCode,attr"`
				} `xml:"BilledQuantity"`
			} `xml:"SpecifiedLineTradeDelivery"`
			Settlement struct {
				LineTotal string `xml:"SpecifiedTradeSettlementLineMonetarySummation>LineTotalAmount"`
			} `xml:"SpecifiedLineTradeSettlement"`
		} `xml:"IncludedSupplyChainTradeLineItem"`
	} `xml:"SupplyChainTradeTransaction"`
}

type ciiParty struct {
	Name string `xml:"Name"`
	Tax  []struct {
		ID string `xml:"ID"`
	} `xml:"SpecifiedTaxRegistration"`
}

func (p ciiParty) party() Party {
	pt := Party{Name: p.Name}
	for _, t := range p.Tax {
		if strings.HasPrefix(t.ID, "DE") || pt.VATID == "" {
			pt.VATID = t.ID
		}
	}
	return pt
}

// ciiDate converts CII "102" format (YYYYMMDD) to ISO.
func ciiDate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) == 8 {
		return s[:4] + "-" + s[4:6] + "-" + s[6:]
	}
	return s
}

// ParseXML parses a standalone e-invoice XML document (UBL or CII).
func ParseXML(data []byte) (*Invoice, error) {
	root := rootLocalName(data)
	switch root {
	case "Invoice", "CreditNote":
		var u ublInvoice
		if err := xml.Unmarshal(data, &u); err != nil {
			return nil, fmt.Errorf("parse UBL: %w", err)
		}
		inv := &Invoice{
			Format:         "ubl",
			Profile:        strings.TrimSpace(u.CustomizationID),
			InvoiceNumber:  strings.TrimSpace(u.ID),
			TypeCode:       strings.TrimSpace(u.InvoiceTypeCode + u.CreditNoteType),
			IssueDate:      strings.TrimSpace(u.IssueDate),
			DueDate:        strings.TrimSpace(u.DueDate),
			Currency:       strings.TrimSpace(u.DocumentCurrency),
			BuyerReference: strings.TrimSpace(u.BuyerReference),
			Seller:         u.Supplier.party(),
			Buyer:          u.Customer.party(),
			TotalNet:       strings.TrimSpace(u.Totals.TaxExclusive),
			TotalTax:       strings.TrimSpace(u.TaxTotal.TaxAmount),
			TotalGross:     strings.TrimSpace(u.Totals.TaxInclusive),
			PayableAmount:  strings.TrimSpace(u.Totals.Payable),
		}
		if inv.TotalNet == "" {
			inv.TotalNet = strings.TrimSpace(u.Totals.LineExtension)
		}
		for _, pm := range u.PaymentMeans {
			if pm.Account.ID != "" && inv.IBAN == "" {
				inv.IBAN = strings.TrimSpace(pm.Account.ID)
			}
			if pm.PaymentID != "" && inv.PaymentReference == "" {
				inv.PaymentReference = strings.TrimSpace(pm.PaymentID)
			}
		}
		for _, l := range append(u.Lines, u.CNLines...) {
			qty, unit := l.Quantity.Value, l.Quantity.Unit
			if strings.TrimSpace(qty) == "" {
				qty, unit = l.CNQuantity.Value, l.CNQuantity.Unit
			}
			inv.Lines = append(inv.Lines, Line{
				ID:          strings.TrimSpace(l.ID),
				Description: strings.TrimSpace(l.Item.Name),
				Quantity:    strings.TrimSpace(qty),
				Unit:        unit,
				NetAmount:   strings.TrimSpace(l.Amount),
			})
		}
		if root == "CreditNote" {
			inv.Format = "ubl-creditnote"
		}
		return inv, nil

	case "CrossIndustryInvoice":
		var c ciiInvoice
		if err := xml.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("parse CII: %w", err)
		}
		inv := &Invoice{
			Format:           "cii",
			Profile:          strings.TrimSpace(c.Context.Guideline),
			InvoiceNumber:    strings.TrimSpace(c.Doc.ID),
			TypeCode:         strings.TrimSpace(c.Doc.TypeCode),
			IssueDate:        ciiDate(c.Doc.IssueDate.DateTimeString),
			DueDate:          ciiDate(c.Trade.Settlement.Terms.DueDate.DateTimeString),
			Currency:         strings.TrimSpace(c.Trade.Settlement.Currency),
			BuyerReference:   strings.TrimSpace(c.Trade.Agreement.BuyerReference),
			PaymentReference: strings.TrimSpace(c.Trade.Settlement.PaymentReference),
			Seller:           c.Trade.Agreement.Seller.party(),
			Buyer:            c.Trade.Agreement.Buyer.party(),
			TotalNet:         strings.TrimSpace(c.Trade.Settlement.Summation.TaxBasis),
			TotalTax:         strings.TrimSpace(c.Trade.Settlement.Summation.TaxTotal),
			TotalGross:       strings.TrimSpace(c.Trade.Settlement.Summation.GrandTotal),
			PayableAmount:    strings.TrimSpace(c.Trade.Settlement.Summation.DuePayable),
		}
		if inv.TotalNet == "" {
			inv.TotalNet = strings.TrimSpace(c.Trade.Settlement.Summation.LineTotal)
		}
		for _, pm := range c.Trade.Settlement.PaymentMeans {
			if pm.IBAN != "" {
				inv.IBAN = strings.TrimSpace(pm.IBAN)
				break
			}
		}
		for _, l := range c.Trade.Lines {
			inv.Lines = append(inv.Lines, Line{
				ID:          strings.TrimSpace(l.Doc.LineID),
				Description: strings.TrimSpace(l.Product.Name),
				Quantity:    strings.TrimSpace(l.Delivery.Quantity.Value),
				Unit:        l.Delivery.Quantity.Unit,
				NetAmount:   strings.TrimSpace(l.Settlement.LineTotal),
			})
		}
		return inv, nil
	}
	return nil, fmt.Errorf("not a recognized e-invoice XML (root element %q; want Invoice/CreditNote (UBL) or CrossIndustryInvoice (CII))", root)
}

// rootLocalName returns the local name of the first start element.
func rootLocalName(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local
		}
	}
}

// knownXMLNames are the standardized embedded-file names for
// ZUGFeRD/Factur-X/XRechnung inside PDF/A-3.
var knownXMLNames = []string{"factur-x.xml", "zugferd-invoice.xml", "ZUGFeRD-invoice.xml", "xrechnung.xml", "cii.xml"}

// ExtractFromPDF pulls the embedded e-invoice XML out of a ZUGFeRD /
// Factur-X PDF. Returns the XML bytes and the embedded file name.
//
// Real-world invoice PDFs ship with broken metadata that makes structured
// extraction fail (measured: a dipasch Factur-X with an invalid ModDate in
// the embedded file spec — pdfcpu knows only Strict/Relaxed validation and
// Relaxed still rejects it). When that happens we fall back to scanning
// raw PDF streams for a deflate-compressed e-invoice XML payload.
func ExtractFromPDF(pdf []byte) ([]byte, string, error) {
	atts, err := api.ExtractAttachmentsRaw(bytes.NewReader(pdf), "", nil, nil)
	if err != nil {
		if x, name, ok := scanStreamsForInvoiceXML(pdf); ok {
			return x, name, nil
		}
		return nil, "", fmt.Errorf("read PDF attachments: %w", err)
	}
	if len(atts) == 0 {
		if x, name, ok := scanStreamsForInvoiceXML(pdf); ok {
			return x, name, nil
		}
		return nil, "", fmt.Errorf("PDF has no embedded files (not a ZUGFeRD/Factur-X invoice)")
	}
	// Prefer standardized names, else any single XML.
	var xmlAtts []int
	for i, a := range atts {
		name := strings.ToLower(a.FileName)
		for _, known := range knownXMLNames {
			if name == strings.ToLower(known) {
				data, err := io.ReadAll(a.Reader)
				if err != nil {
					return nil, "", err
				}
				return data, a.FileName, nil
			}
		}
		if strings.HasSuffix(name, ".xml") {
			xmlAtts = append(xmlAtts, i)
		}
	}
	if len(xmlAtts) == 1 {
		a := atts[xmlAtts[0]]
		data, err := io.ReadAll(a.Reader)
		if err != nil {
			return nil, "", err
		}
		return data, a.FileName, nil
	}
	if x, name, ok := scanStreamsForInvoiceXML(pdf); ok {
		return x, name, nil
	}
	names := make([]string, len(atts))
	for i, a := range atts {
		names[i] = a.FileName
	}
	return nil, "", fmt.Errorf("no e-invoice XML among embedded files: %s", strings.Join(names, ", "))
}

// scanStreamsForInvoiceXML walks every raw stream object in the PDF and
// returns the first one that is (possibly deflate-compressed) e-invoice
// XML. It ignores PDF structure entirely, so broken metadata cannot stop
// it; false positives are ruled out by the root-element check.
func scanStreamsForInvoiceXML(pdf []byte) ([]byte, string, bool) {
	rest := pdf
	for {
		i := bytes.Index(rest, []byte("stream"))
		if i < 0 {
			return nil, "", false
		}
		// Skip the "stream" substring inside "endstream" keywords.
		if i >= 3 && bytes.Equal(rest[i-3:i], []byte("end")) {
			rest = rest[i+len("stream"):]
			continue
		}
		seg := rest[i+len("stream"):]
		seg = bytes.TrimPrefix(seg, []byte("\r\n"))
		seg = bytes.TrimPrefix(seg, []byte("\n"))
		end := bytes.Index(seg, []byte("endstream"))
		if end < 0 {
			return nil, "", false
		}
		if x, ok := tryInvoiceXML(bytes.TrimRight(seg[:end], "\r\n")); ok {
			return x, "embedded XML (raw stream scan)", true
		}
		rest = seg[end:]
	}
}

func tryInvoiceXML(data []byte) ([]byte, bool) {
	candidates := [][]byte{data}
	if zr, err := zlib.NewReader(bytes.NewReader(data)); err == nil {
		if d, err := io.ReadAll(zr); err == nil {
			candidates = append(candidates, d)
		}
		zr.Close()
	}
	for _, c := range candidates {
		trimmed := bytes.TrimLeft(c, "\xef\xbb\xbf \r\n\t")
		if !bytes.HasPrefix(trimmed, []byte("<?xml")) && !bytes.HasPrefix(trimmed, []byte("<")) {
			continue
		}
		switch rootLocalName(trimmed) {
		case "Invoice", "CreditNote", "CrossIndustryInvoice":
			return trimmed, true
		}
	}
	return nil, false
}
