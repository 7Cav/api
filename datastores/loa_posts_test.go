package datastores_test

// LOA opening posts as the forum stores them (xf_post.message), one per LOA
// form template plus a hand-posted template copy. Each is a copy of a stored
// post with the names replaced: the Subject is loaAlpha (user 9001) unless a
// sample says otherwise, and mention ids point at the member they name.
// Markup, labels, COLOR values, spacing, the form-12 "$" and the mixed
// "\r\n"/"\n" line endings are byte-for-byte the stored post. Dates are kept
// in the post's own layout. Tests change at most one field of a sample.

// Form 2, 1st Battalion LOA (node 180), mention-era template.
const loaPostForm2Node180 = "" +
	"\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Rank[/COLOR]:[/B] PFC\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Username[/COLOR][/B]: [USER=9001]Loa.Alpha[/USER]\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Primary Billet[/COLOR][/B]: A/1-7\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Reason:[/COLOR][/B]\n" +
	"Family trip.\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Start Date[/COLOR][/B] \n" +
	"Oct 10, 2026\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]End Date[/COLOR][/B] \n" +
	"Oct 23, 2026\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Request for Self[/COLOR][/B] \n" +
	"Yes\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]I understand an LOA excludes me from no more than 2 weeks of duties and responsibilities not to exceed 14 days total.[/COLOR][/B] \n" +
	"Yes\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]I have contacted the Chain of Command before submitting this request[/COLOR][/B] \n" +
	"Yes\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]I understand a Cav week begins on a Saturday and ends the following Friday.[/COLOR][/B] Yes\r\n" +
	"\r\n" +
	"[HR][/HR]\n" +
	"[B][COLOR=rgb(213, 185, 0)]Submitter Name[/COLOR][/B] \n" +
	"Loa.Alpha"

// Form 4, 2nd Battalion LOA (node 178).
const loaPostForm4Node178 = "" +
	"\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Rank[/COLOR]:[/B] PFC\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Username[/COLOR][/B]: [USER=9001]Loa.Alpha[/USER]\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Primary Billet[/COLOR][/B]: A/2-7\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Reason:[/COLOR][/B]\n" +
	"Family trip.\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Start Date[/COLOR][/B] \n" +
	"Oct 10, 2026\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]End Date[/COLOR][/B] \n" +
	"Oct 16, 2026\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Request for Self[/COLOR][/B] \n" +
	"Yes\r\n" +
	"\r\n" +
	"[HR][/HR]\n" +
	"[B][COLOR=rgb(213, 185, 0)]I understand an LOA excludes me from no more than 2 weeks of duties and responsibilities not to exceed 14 days total.[/COLOR][/B] \n" +
	"Yes\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]I have informed my immediate Chain of Command prior to submitting this request[/COLOR][/B] \n" +
	"Yes\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]I understand a Cav week begins on a Saturday and ends the following Friday.[/COLOR][/B] Yes\r\n" +
	"\r\n" +
	"[HR][/HR]\n" +
	"[B][COLOR=rgb(213, 185, 0)]Submitter Name[/COLOR][/B] \n" +
	"Loa.Alpha"

// Form 11, Regimental LOA (node 369): multi-line reason, empty Submitter Name.
const loaPostForm11Node369 = "" +
	"\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Rank[/COLOR]:[/B] PFC\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Username[/COLOR][/B]: [USER=9001]Loa.Alpha[/USER]\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Primary Billet[/COLOR][/B]: S6\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Billet Role[/COLOR][/B] Other\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Reason:[/COLOR][/B] \n" +
	"Family trip.\n" +
	"\n" +
	"Thanks,\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Start Date[/COLOR][/B] \n" +
	"Oct 10, 2026\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]End Date[/COLOR][/B] \n" +
	"Oct 24, 2026\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]I understand an LOA excludes me from no more than 2 weeks of duties and responsibilities not to exceed 14 days total.[/COLOR][/B] \n" +
	"Yes\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]I have informed my immediate Chain of Command prior to submitting this request[/COLOR][/B] \n" +
	"Yes\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]I understand a Cav week begins on a Saturday and ends the following Friday.[/COLOR][/B] Yes\r\n" +
	"\r\n" +
	"[HR][/HR]\n" +
	"[B][COLOR=rgb(213, 185, 0)]Submitter Name[/COLOR][/B] \n"

// Form 12, ACD LOA (node 400), filed for someone else. The first Username
// label is the Submitter (loaAlpha, added by the form's include-name option);
// the Username question below names the Subject (loaBravo) as a mention with
// the form's stray "$".
const loaPostForm12Node400FiledForOther = "" +
	"[COLOR=rgb(213, 185, 0)][B]Username:[/B] [/COLOR]Loa.Alpha\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Rank[/COLOR][/B]: PFC\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Username[/COLOR][/B]: $[USER=9002]Loa.Bravo[/USER]\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Primary Billet[/COLOR][/B]: A/ACD\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Reason:[/COLOR][/B]\n" +
	"Family trip.\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Start Date[/COLOR][/B] \n" +
	"Oct 9, 2026\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]End Date[/COLOR][/B] \n" +
	"Oct 23, 2026\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Request for Self[/COLOR][/B] \n" +
	"No\r\n" +
	"\r\n" +
	"[HR][/HR]\n" +
	"[B][COLOR=rgb(213, 185, 0)]Submitter Name[/COLOR][/B] \n" +
	"Loa.Alpha\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]I understand an LOA excludes me from no more than 2 weeks of duties and responsibilities not to exceed 14 days total.[/COLOR][/B] \n" +
	"Yes\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]I have contacted the Chain of Command before submitting this request[/COLOR][/B] \n" +
	"Yes\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]I understand a Cav week begins on a Saturday and ends the following Friday.[/COLOR][/B] Yes"

// Form 16, 3rd Battalion LOA (node 540): no colon after Username, and the
// Submitter Name (loaBravo) differs from the Subject.
const loaPostForm16Node540 = "" +
	"\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Rank[/COLOR]:[/B] PFC\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Username[/COLOR][/B] [USER=9001]Loa.Alpha[/USER]\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Primary Billet[/COLOR][/B]: B/3-7\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Reason:[/COLOR][/B] \n" +
	"Family trip.\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Start Date[/COLOR][/B] \n" +
	"Oct 3, 2026\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]End Date[/COLOR][/B] \n" +
	"Oct 16, 2026\r\n" +
	"\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]Request for Self[/COLOR][/B] \n" +
	"No\r\n" +
	"\r\n" +
	"[HR][/HR]\n" +
	"[B][COLOR=rgb(213, 185, 0)]I understand an LOA excludes me from no more than 2 weeks of duties and responsibilities not to exceed 14 days total.[/COLOR][/B] \n" +
	"Yes\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]I have informed my immediate Chain of Command prior to submitting this request[/COLOR][/B] \n" +
	"Yes\r\n" +
	"[B][COLOR=rgb(213, 185, 0)]I understand a Cav week begins on a Saturday and ends the following Friday.[/COLOR][/B] Yes\r\n" +
	"\r\n" +
	"[HR][/HR]\n" +
	"[B][COLOR=rgb(213, 185, 0)]Submitter Name[/COLOR][/B] \n" +
	"Loa.Bravo"

// Hand-posted thread (node 400): a copy of the ACD form's text without the
// COLOR tags, LF-only, posted by the member with no form submission behind it.
const loaPostHandPostedTemplateCopy = "" +
	"[B]Rank:[/B] PFC\n" +
	"\n" +
	"[B]Username[/B]: Loa.Alpha\n" +
	"\n" +
	"[B]Primary Billet[/B]: A/ACD\n" +
	"\n" +
	"[B]Reason:[/B]\n" +
	"Family trip.\n" +
	"\n" +
	"[B]Start Date[/B]\n" +
	"Sep 13, 2026\n" +
	"[B]End Date[/B]\n" +
	"Sep 27, 2026\n" +
	"\n" +
	"[B]Request for Self[/B]\n" +
	"Yes\n" +
	"[B]I understand an LOA excludes me from no more than 2 weeks of duties and responsibilities not to exceed 14 days total.[/B]\n" +
	"Yes\n" +
	"\n" +
	"[B]I have contacted the Chain of Command before submitting this request[/B]\n" +
	"Yes\n" +
	"[B]I understand a Cav week begins on a Saturday and ends the following Friday.[/B] Yes"
